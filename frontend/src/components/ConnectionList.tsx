// ConnectionList - 连线清单面板
// 列出当前拓扑所有连线，点击可高亮并定位到画布对应连线，× 按钮可删除。
import { type Link, type Device, getLinkTypeLabel } from '../types';

interface ConnectionListProps {
  links: Link[];
  devices: Record<string, Device>;
  selectedLinkId: string | null;
  onSelect: (linkId: string) => void;
  onLocate: (linkId: string) => void;
  onDelete: (linkId: string) => void;
}

function deviceName(devices: Record<string, Device>, id: string): string {
  return devices[id]?.name || id;
}

export default function ConnectionList({
  links,
  devices,
  selectedLinkId,
  onSelect,
  onLocate,
  onDelete,
}: ConnectionListProps) {
  const handleRowClick = (id: string) => {
    onSelect(id);
    onLocate(id);
  };

  return (
    <div className="connection-list">
      <div className="connection-list-body">
        {links.length === 0 ? (
          <div className="connection-list-empty">暂无连线</div>
        ) : (
          links.map((l) => {
            const srcName = deviceName(devices, l.source_device);
            const dstName = deviceName(devices, l.target_device);
            const typeLabel = getLinkTypeLabel(l.link_type);
            const vniPart = l.link_type === 'vxlan' && l.vxlan_vni ? `, VNI: ${l.vxlan_vni}` : '';
            const detail = `${l.source_port}:${l.target_port}, 类型: ${typeLabel}${vniPart}`;
            const active = l.id === selectedLinkId;
            return (
              <div
                key={l.id}
                className={`connection-item${active ? ' active' : ''}`}
                onClick={() => handleRowClick(l.id)}
                title="点击高亮并定位到该连线"
              >
                <div className="connection-item-main">
                  <span className="connection-item-route">
                    {srcName} <span className="arrow">→</span> {dstName}
                  </span>
                  <button
                    className="connection-del-btn"
                    title="删除连线"
                    onClick={(e) => {
                      e.stopPropagation();
                      onDelete(l.id);
                    }}
                  >
                    ×
                  </button>
                </div>
                <div className="connection-item-detail">{detail}</div>
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}
