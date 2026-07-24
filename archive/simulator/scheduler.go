//go:build ignore

// Package simulator 提供网络仿真核心：事件调度器、离散事件循环与设备模拟。
//
// scheduler.go 在 engine.go 之上提供统一的事件驱动抽象：
//   - Scheduler 维护优先级事件队列，按虚拟时间顺序执行；
//   - 通过 Subscribe 接口将事件总线接入 API 网关 / 前端可视化；
//   - Run() / Stop() 控制仿真循环生命周期。
package simulator

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// EventKind 描述事件类型。
type EventKind string

const (
	EventPacketArrive EventKind = "packet_arrive"
	EventPacketDrop   EventKind = "packet_drop"
	EventPacketSent   EventKind = "packet_sent"
	EventDeviceUp     EventKind = "device_up"
	EventDeviceDown   EventKind = "device_down"
	EventLinkUp       EventKind = "link_up"
	EventLinkDown     EventKind = "link_down"
	EventTimer        EventKind = "timer"
	EventCustom       EventKind = "custom"
)

// Event 表示一次仿真事件，包含计划时间与具体负载。
type Event struct {
	At      time.Time
	Kind    EventKind
	Payload any
}

// Subscriber 事件订阅者签名。
type Subscriber func(ev Event)

// Scheduler 是离散事件调度器，负责推进仿真时间。
//
// 并发模型：
//   - 单一 goroutine 消费事件队列（消费者）；
//   - ScheduleXxx() 方法可被任意 goroutine 调用（生产者），内部使用互斥锁保护；
//   - 订阅者回调在消费者 goroutine 中同步执行；
//   - Run() 启动消费者循环；ctx 取消或调用 Stop() 优雅停止。
type Scheduler struct {
	mu       sync.Mutex
	queue    []*Event
	wake     chan struct{}
	stop     chan struct{}
	done     chan struct{}
	closed   bool
	stopOnce sync.Once

	clockMu  sync.RWMutex
	clock    time.Time
	stepSize time.Duration

	subsMu sync.RWMutex
	subs   []Subscriber
}

// NewScheduler 构造一个尚未启动的调度器，stepSize 是 Run 模式下的最小推进粒度。
func NewScheduler(stepSize time.Duration) *Scheduler {
	if stepSize <= 0 {
		stepSize = 100 * time.Millisecond
	}
	return &Scheduler{
		queue:    make([]*Event, 0, 64),
		wake:     make(chan struct{}, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		clock:    time.Unix(0, 0).UTC(),
		stepSize: stepSize,
	}
}

// Subscribe 注册事件订阅者，返回取消函数。
func (s *Scheduler) Subscribe(fn Subscriber) func() {
	s.subsMu.Lock()
	s.subs = append(s.subs, fn)
	idx := len(s.subs) - 1
	s.subsMu.Unlock()
	return func() {
		s.subsMu.Lock()
		defer s.subsMu.Unlock()
		if idx < len(s.subs) {
			s.subs[idx] = nil
		}
	}
}

// Now 返回当前仿真时间。
func (s *Scheduler) Now() time.Time {
	s.clockMu.RLock()
	defer s.clockMu.RUnlock()
	return s.clock
}

// SetClock 强制将仿真时间设置为指定值（用于调试或回放）。
func (s *Scheduler) SetClock(t time.Time) {
	s.clockMu.Lock()
	s.clock = t.UTC()
	s.clockMu.Unlock()
}

// Schedule 在指定时间发布一个事件。时间为零值时表示立即（Next tick）。
func (s *Scheduler) Schedule(at time.Time, kind EventKind, payload any) error {
	if s.isClosed() {
		return errors.New("scheduler: closed")
	}
	ev := &Event{At: at.UTC(), Kind: kind, Payload: payload}
	s.mu.Lock()
	s.queue = append(s.queue, ev)
	// 简单插入排序维护升序（小队列 O(n) 即可）
	s.sortLocked()
	s.mu.Unlock()
	s.signal()
	return nil
}

// ScheduleAfter 在 from + delay 时发布事件。
func (s *Scheduler) ScheduleAfter(delay time.Duration, kind EventKind, payload any) error {
	return s.Schedule(s.Now().Add(delay), kind, payload)
}

// ScheduleFunc 在 delay 后调用 fn（封装为 timer 事件）。
func (s *Scheduler) ScheduleFunc(delay time.Duration, fn func()) error {
	return s.ScheduleAfter(delay, EventTimer, fn)
}

// Run 启动事件循环；ctx 取消时优雅停止。
func (s *Scheduler) Run(ctx context.Context) {
	defer close(s.done)

	ticker := time.NewTicker(s.stepSize)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-s.wake:
			s.drain()
		case <-ticker.C:
			s.drain()
		}
	}
}

// Stop 停止事件循环。多次调用安全。
func (s *Scheduler) Stop() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.stop)
		// 仅当 Run 被调用过才等待 done
		select {
		case <-s.done:
		default:
			// Run 尚未启动；只需确保 stop 已关闭即可
		}
	})
}

// sortLocked 在持锁状态下对事件队列按时间升序排序。
func (s *Scheduler) sortLocked() {
	// 插入排序：队列极短（典型 < 100），O(n^2) 可接受。
	for i := 1; i < len(s.queue); i++ {
		for j := i; j > 0 && s.queue[j-1].At.After(s.queue[j].At); j-- {
			s.queue[j-1], s.queue[j] = s.queue[j], s.queue[j-1]
		}
	}
}

// signal 非阻塞地唤醒事件循环。
func (s *Scheduler) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// drain 弹出所有已到期事件并按时间顺序 dispatch。
// 事件到期判断使用真实墙钟时间（time.Now()），因此 Schedule 传入的 At 应当基于真实时间。
func (s *Scheduler) drain() {
	for {
		s.mu.Lock()
		if len(s.queue) == 0 {
			s.mu.Unlock()
			return
		}
		head := s.queue[0]
		now := time.Now()
		if head.At.After(now) {
			s.mu.Unlock()
			return
		}
		s.queue = s.queue[1:]
		s.mu.Unlock()

		// 推进仿真时间
		s.clockMu.Lock()
		if head.At.After(s.clock) {
			s.clock = head.At
		}
		s.clockMu.Unlock()

		s.dispatch(head)
	}
}

// dispatch 通知所有订阅者。
func (s *Scheduler) dispatch(ev *Event) {
	s.subsMu.RLock()
	subs := make([]Subscriber, len(s.subs))
	copy(subs, s.subs)
	s.subsMu.RUnlock()
	for _, fn := range subs {
		if fn != nil {
			fn(*ev)
		}
	}
}

// isClosed 检查调度器是否已关闭。
func (s *Scheduler) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// String 返回事件的可读表示。
func (e Event) String() string {
	return fmt.Sprintf("Event{kind=%s at=%s}", e.Kind, e.At.Format(time.RFC3339Nano))
}
