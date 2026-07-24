//go:build !gont

package router

import (
	"fmt"
)

type Router interface {
	Start() error
	Stop() error
	ApplyOSPFConfig(network string, area string) error
	ApplyBGPConfig(localAS uint32, neighbors []BGPNeighbor) error
	IsRunning() bool
	GetRoutes() ([]RouteInfo, error)
}

type BGPNeighbor struct {
	IP       string
	RemoteAS uint32
}

type RouteInfo struct {
	Destination string
	NextHop     string
	Metric      int
	Protocol    string
}

type FRRRouter struct{}

func NewFRRRouter(host interface{}, name string) *FRRRouter {
	return &FRRRouter{}
}

func (r *FRRRouter) Start() error {
	return fmt.Errorf("router: FRR requires Linux")
}

func (r *FRRRouter) Stop() error {
	return nil
}

func (r *FRRRouter) ApplyOSPFConfig(network string, area string) error {
	return fmt.Errorf("router: FRR requires Linux")
}

func (r *FRRRouter) ApplyBGPConfig(localAS uint32, neighbors []BGPNeighbor) error {
	return fmt.Errorf("router: FRR requires Linux")
}

func (r *FRRRouter) IsRunning() bool {
	return false
}

func (r *FRRRouter) GetRoutes() ([]RouteInfo, error) {
	return nil, fmt.Errorf("router: FRR requires Linux")
}
