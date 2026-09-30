import { Button, Radio, Spin } from "@arco-design/web-react";
import { Fragment, useEffect, useState, type ReactNode } from "react";
import folderIcon from "../../assets/icons8-folder.svg";

/** 目录条目；key 回传给调用方，disabled 表示该目录已被占用、不可再选，path 是选中时展示的完整路径。 */
export type DirectoryPickerEntry = { key: string; name: string; disabled?: boolean; path?: string };

/** 面包屑节点；最后一项是当前目录，其余节点可点击回退。 */
export type DirectoryPickerCrumb = { key: string; name: string };

/** 目录列表状态；加载与错误文案由调用方提供，组件只负责展示。 */
export type DirectoryPickerStatus = "loading" | "error" | "ready";

/** 目录展示方式：列表对应图一，宫格对应图二。 */
export type DirectoryPickerView = "list" | "grid";

/**
 * 通用目录选择器。
 *
 * 结构固定为「面包屑 + 列表头 + 目录区 + 附加区 + 底部操作」：面包屑显示当前路径
 * 且末级为蓝色，单击目录选中、再次单击取消选中、双击进入下一级；只有选中目录后「确认」才可用。
 * 点击末级蓝色路径表示选中当前目录（同样可再次点击取消），因此根目录也能被选中。
 * 目录图标统一使用 icons8 文件夹图片，不展示日期与灰色底；选中的目录带高亮背景与边框，
 * 底部展示「当前选择: 完整路径」，未选中时不显示。
 * 数据、状态与导航全部由调用方注入，115 网盘、CloudDrive2 与本地 strm 目录共用这一份实现。
 */
export function DirectoryPicker({
  crumbs,
  onNavigate,
  onEnter,
  entries,
  status,
  errorText = "目录读取失败",
  emptyText = "当前目录下没有子目录",
  loadingText = "正在读取目录…",
  currentOccupied = false,
  currentPath,
  onConfirm,
  onCancel,
  confirmLabel = "确认",
  toolbar,
  extra,
  footer,
}: {
  crumbs: DirectoryPickerCrumb[];
  onNavigate: (crumb: DirectoryPickerCrumb) => void;
  onEnter: (entry: DirectoryPickerEntry) => void;
  entries: DirectoryPickerEntry[];
  status: DirectoryPickerStatus;
  errorText?: string;
  emptyText?: string;
  loadingText?: string;
  currentOccupied?: boolean;
  /** 当前目录的完整展示路径；未传时用面包屑末级名称代替。 */
  currentPath?: string;
  onConfirm: (entry: DirectoryPickerEntry) => void;
  onCancel: () => void;
  confirmLabel?: string;
  toolbar?: ReactNode;
  extra?: ReactNode;
  footer?: ReactNode;
}) {
  const [view, setView] = useState<DirectoryPickerView>("list");
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const current = crumbs[crumbs.length - 1];
  const currentKey = current?.key ?? "";

  // 进入新目录后清空选中：上一级的选中项不能带到下一级，否则会选错目录。
  useEffect(() => setSelectedKey(null), [currentKey]);

  // 选中项必须仍在当前列表里（翻页或刷新后消失即视为未选中），当前目录单独判定。
  const selected =
    selectedKey === null
      ? undefined
      : selectedKey === currentKey
        ? {
            key: currentKey,
            name: current?.name ?? "",
            disabled: currentOccupied,
            path: currentPath ?? current?.name ?? "",
          }
        : entries.find((entry) => entry.key === selectedKey);
  // 底部只展示选中项的完整路径；未选中时不显示文案。
  const selectedLabel = selected ? `当前选择: ${selected.path ?? selected.name}` : "";

  return (
    <div className="directory-picker">
      <nav className="directory-picker-crumbs" aria-label="目录路径">
        {crumbs.map((crumb, index) => {
          const last = index === crumbs.length - 1;
          return (
            <Fragment key={crumb.key}>
              {index > 0 ? <span className="directory-picker-crumb-separator">/</span> : null}
              <Button
                type="text"
                size="mini"
                className={last ? "directory-picker-crumb-current" : undefined}
                disabled={last && currentOccupied}
                title={last ? (currentOccupied ? "该目录已添加" : "选中当前目录，再次点击取消") : `返回 ${crumb.name}`}
                onClick={() =>
                  last ? setSelectedKey(selectedKey === crumb.key ? null : crumb.key) : onNavigate(crumb)
                }
              >
                {crumb.name}
              </Button>
            </Fragment>
          );
        })}
      </nav>
      <div className="directory-picker-head">
        <span className="directory-picker-head-title">文件名</span>
        <span className="directory-picker-head-summary">{entries.length}个文件夹，0个文件</span>
        <div className="directory-picker-head-actions">
          {toolbar}
          <Radio.Group type="button" size="mini" value={view} onChange={(value) => setView(value as DirectoryPickerView)}>
            <Radio value="list">列表</Radio>
            <Radio value="grid">宫格</Radio>
          </Radio.Group>
        </div>
      </div>
      {/* 目录区固定最小高度，空目录与加载中不会把弹窗压扁；状态文案在区域内居中。 */}
      <div className="directory-picker-body">
        {status === "loading" ? (
          <div className="directory-picker-state"><Spin size={16} /> {loadingText}</div>
        ) : status === "error" ? (
          <div className="directory-picker-state">{errorText}</div>
        ) : entries.length === 0 ? (
          <div className="directory-picker-state">{emptyText}</div>
        ) : (
          <ul className={`directory-picker-entries directory-picker-entries-${view}`}>
            {entries.map((entry) => (
              <li key={entry.key}>
                <button
                  type="button"
                  className={
                    selectedKey === entry.key
                      ? "directory-picker-entry directory-picker-entry-selected"
                      : "directory-picker-entry"
                  }
                  disabled={entry.disabled}
                  aria-pressed={selectedKey === entry.key}
                  onClick={() => setSelectedKey(selectedKey === entry.key ? null : entry.key)}
                  onDoubleClick={() => onEnter(entry)}
                >
                  <img className="directory-picker-icon" src={folderIcon} alt="" />
                  <span className="directory-picker-name">{entry.name}</span>
                  {entry.disabled ? <span className="directory-picker-badge" aria-hidden="true">已添加</span> : null}
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
      {extra}
      {footer}
      <div className="directory-picker-footer">
        <span className="directory-picker-selection">{selectedLabel}</span>
        <div className="settings-pan115-actions">
          <Button type="secondary" onClick={onCancel}>取消</Button>
          <Button type="primary" disabled={!selected || selected.disabled} onClick={() => selected && onConfirm(selected)}>
            {confirmLabel}
          </Button>
        </div>
      </div>
    </div>
  );
}
