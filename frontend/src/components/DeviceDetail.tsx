// DeviceDetail - 浮动窗口内部内容（Tab 切换布局）
//
// 作为 FloatWindow 的内容注入：只负责 CLI / 配置 两个 Tab 的切换，
// 不再渲染固定底部面板的外层 header（标题栏由 FloatWindow 提供）。
//
// 设计要点：
//   - 默认显示 CLI Tab
//   - 两个子组件始终挂载（仅通过 display 切换可见性），切换 Tab 时
//     各自内部状态（CLI 输入/历史、配置表单值）均被保留
//   - 未选中设备时（理论上窗口只对有设备时打开）不渲染
import { useState } from 'react';
import { type Device } from '../types';
import CliTerminal from './CliTerminal';
import DeviceConfigPanel from './DeviceConfigPanel';

interface DeviceDetailProps {
  topologyId: string | null;
  // 窗口只为真实存在的设备打开，故为必填
  selectedDevice: Device;
  onConfigApplied?: () => void;
  // CLI 中执行 telnet/进入其他设备时回调，用于跳转到目标设备的浮动窗口
  onTargetDevice?: (deviceId: string) => void;
}

type TabKey = 'cli' | 'config';

export default function DeviceDetail(props: DeviceDetailProps) {
  const { topologyId, selectedDevice, onConfigApplied, onTargetDevice } = props;
  const [activeTab, setActiveTab] = useState<TabKey>('cli');

  return (
    <div className="device-detail-win">
      <div className="device-detail-tabs" role="tablist">
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === 'cli'}
          className={`device-tab ${activeTab === 'cli' ? 'device-tab-active' : ''}`}
          onClick={() => setActiveTab('cli')}
        >
          CLI
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === 'config'}
          className={`device-tab ${activeTab === 'config' ? 'device-tab-active' : ''}`}
          onClick={() => setActiveTab('config')}
        >
          配置
        </button>
      </div>

      <div className="device-detail-body">
        {/* CLI Tab：始终保持挂载以保留会话状态 */}
        <div className={activeTab === 'cli' ? 'device-detail-pane' : 'device-detail-pane device-detail-pane-hidden'}>
          <CliTerminal
            topologyId={topologyId}
            selectedDevice={selectedDevice}
            onTargetDevice={onTargetDevice}
          />
        </div>

        {/* 配置 Tab：始终保持挂载以保留表单输入 */}
        <div className={activeTab === 'config' ? 'device-detail-pane' : 'device-detail-pane device-detail-pane-hidden'}>
          <DeviceConfigPanel
            topologyId={topologyId}
            selectedDevice={selectedDevice}
            onApplied={() => onConfigApplied?.()}
          />
        </div>
      </div>
    </div>
  );
}
