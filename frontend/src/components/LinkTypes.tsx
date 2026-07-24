// LinkTypes - 连线种类选择器（左侧“连线种类” Tab）
// 列出所有可选择的连线类型（含 auto 自动），点击设为当前连线类型；
// 之后进入“连线”模式从一台设备拖到另一台即按所选类型创建。
import { type LinkTypeMode, LINK_TYPE_MODES } from '../types';

interface LinkTypesProps {
  activeType: LinkTypeMode;
  onSelect: (t: LinkTypeMode) => void;
}

export default function LinkTypes({ activeType, onSelect }: LinkTypesProps) {
  return (
    <div className="link-types">
      <div className="lt-hint">
        选择连线类型后，进入顶部「连线」模式，从源设备拖到目标设备即可创建。选
        “自动”时按设备角色自动匹配类型。
      </div>
      {LINK_TYPE_MODES.map((m) => (
        <button
          key={m.value}
          type="button"
          className={`link-type-card${activeType === m.value ? ' lt-active' : ''}`}
          onClick={() => onSelect(m.value)}
          title={m.hint}
        >
          <span
            className="lt-preview"
            style={{
              borderTop: `${m.dash.length ? '3px dashed' : '3px solid'} ${m.color}`,
            }}
          />
          <span className="lt-label">{m.label}</span>
          <span className="lt-hint-small">{m.hint}</span>
        </button>
      ))}
    </div>
  );
}
