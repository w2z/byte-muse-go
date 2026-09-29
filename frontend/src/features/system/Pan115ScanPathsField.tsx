import { Button, Input, Modal, Spin } from "@arco-design/web-react";
import { useQuery } from "@tanstack/react-query";
import { Fragment, useState } from "react";
import { apiRequest } from "../../shared/api/client";

/** 一个扫描目录；id 是 115 目录 ID，path 是展示用的完整路径。 */
export type Pan115ScanPath = { id: string; path: string };

/** 115 目录条目；只有 is_directory 为 true 的条目可以进入下一级。 */
type Pan115FileEntry = {
  id: string;
  parent_id: string;
  name: string;
  is_directory: boolean;
  size: number;
  pick_code: string;
};

/** 一页 115 目录内容；path 是从根目录到当前目录的完整路径。 */
type Pan115FilePage = {
  directory_id: string;
  path: { id: string; name: string }[];
  files: Pan115FileEntry[];
  total: number;
  has_more: boolean;
};

/** 115 用 "0" 表示网盘根目录。 */
const rootDirectoryID = "0";
/** 目录分页大小；后端默认 100、上限 1000，选择目录固定使用默认值。 */
const pickerPageSize = 100;

/** 把 115 返回的目录路径拼成展示用绝对路径；根目录为 "/"。 */
function pathLabel(path: { name: string }[]): string {
  const names = path.map((item) => item.name.trim()).filter((name) => name !== "");
  return names.length === 0 ? "/" : `/${names.join("/")}`;
}

/**
 * 115 扫描目录字段。
 *
 * 每个目录一行：只读输入框展示已选路径，右侧按钮负责选择与删除，
 * 避免手填目录 ID 产生无效配置；支持添加多个目录。
 * 草稿由设置页统一持有并随「保存设置」提交，组件本身不落库。
 */
export function Pan115ScanPathsField({
  value,
  onChange,
}: {
  value: Pan115ScanPath[];
  onChange: (next: Pan115ScanPath[]) => void;
}) {
  // index 为 null 表示新增目录，非 null 表示替换该下标的目录。
  const [picking, setPicking] = useState<{ index: number | null } | null>(null);
  // 每次打开都换 key 重建选择器：弹窗关闭后 DOM 可能被保留，靠 key 保证每次都从根目录开始。
  const [pickerRun, setPickerRun] = useState(0);

  const openPicker = (index: number | null) => {
    setPickerRun((previous) => previous + 1);
    setPicking({ index });
  };

  const select = (path: Pan115ScanPath) => {
    const index = picking?.index ?? null;
    onChange(index === null ? [...value, path] : value.map((item, position) => (position === index ? path : item)));
    setPicking(null);
  };

  // 编辑某一行时该行自己的目录不算重复，允许原样保留。
  const selectedIDs = value.filter((_, position) => position !== picking?.index).map((item) => item.id);

  return (
    <div className="settings-pan115-paths">
      {value.length === 0 ? (
        <span className="settings-field-placeholder">尚未添加扫描目录</span>
      ) : (
        <ul className="settings-pan115-path-list">
          {value.map((item, index) => (
            <li className="settings-pan115-path-row" key={`${item.id}-${index}`}>
              <Input readOnly value={item.path} aria-label={`扫描目录 ${index + 1}`} />
              <Button type="secondary" onClick={() => openPicker(index)}>选择目录</Button>
              <Button
                type="secondary"
                status="danger"
                onClick={() => onChange(value.filter((_, position) => position !== index))}
              >
                删除
              </Button>
            </li>
          ))}
        </ul>
      )}
      <div className="settings-pan115-actions">
        <Button type="primary" onClick={() => openPicker(null)}>添加目录</Button>
      </div>
      <Modal
        title="选择 115 目录"
        visible={picking !== null}
        footer={null}
        unmountOnExit
        maskClosable={false}
        onCancel={() => setPicking(null)}
      >
        <Pan115DirectoryPicker
          key={pickerRun}
          selectedIDs={selectedIDs}
          onCancel={() => setPicking(null)}
          onSelect={select}
        />
      </Modal>
    </div>
  );
}

/**
 * 115 目录选择弹窗内容：面包屑导航、子目录列表、分页与「选择当前目录」。
 * 只列出文件夹，文件条目不可进入；未绑定账号时后端返回 409，这里直接展示后端文案。
 */
function Pan115DirectoryPicker({
  selectedIDs,
  onCancel,
  onSelect,
}: {
  selectedIDs: string[];
  onCancel: () => void;
  onSelect: (path: Pan115ScanPath) => void;
}) {
  const [location, setLocation] = useState({ id: rootDirectoryID, page: 1 });
  const listing = useQuery({
    queryKey: ["pan115-files", location.id, location.page],
    queryFn: () =>
      apiRequest<Pan115FilePage>(
        `/pan115/files?directory_id=${encodeURIComponent(location.id)}&offset=${(location.page - 1) * pickerPageSize}&limit=${pickerPageSize}`,
      ),
    retry: false,
  });

  const navigate = (id: string, page = 1) => setLocation({ id, page });
  const page = listing.data;
  const currentPath = page ? pathLabel(page.path) : "/";
  const directories = page?.files.filter((file) => file.is_directory) ?? [];
  const added = selectedIDs.includes(location.id);
  const crumbs = page?.path ?? [];

  return (
    <div className="settings-pan115-picker">
      <nav className="settings-pan115-breadcrumbs" aria-label="115 目录路径">
        {crumbs.map((item, index) => (
          <Fragment key={item.id}>
            {index > 0 ? <span className="settings-pan115-breadcrumb-separator">/</span> : null}
            <Button type="text" size="mini" disabled={index === crumbs.length - 1} onClick={() => navigate(item.id)}>
              {item.name || "根目录"}
            </Button>
          </Fragment>
        ))}
      </nav>
      {listing.isLoading ? (
        <div className="settings-pan115-picker-state"><Spin size={16} /> 正在读取 115 目录…</div>
      ) : listing.isError ? (
        <div className="settings-pan115-picker-state">{listing.error.message}</div>
      ) : directories.length === 0 ? (
        <div className="settings-pan115-picker-state">当前目录下没有子文件夹</div>
      ) : (
        <ul className="settings-pan115-picker-list">
          {directories.map((item) => (
            <li key={item.id}>
              <Button type="text" long className="settings-pan115-picker-item" onClick={() => navigate(item.id)}>
                {item.name}
              </Button>
            </li>
          ))}
        </ul>
      )}
      <div className="settings-pan115-picker-footer">
        <span className="settings-field-description">
          第 {location.page} 页{page ? ` · 共 ${page.total} 项` : ""}
        </span>
        <div className="settings-pan115-actions">
          <Button
            type="secondary"
            disabled={location.page === 1 || listing.isFetching}
            onClick={() => navigate(location.id, location.page - 1)}
          >
            上一页
          </Button>
          <Button
            type="secondary"
            disabled={!page?.has_more || listing.isFetching}
            onClick={() => navigate(location.id, location.page + 1)}
          >
            下一页
          </Button>
        </div>
      </div>
      <div className="settings-pan115-picker-footer">
        <span className="settings-field-description">当前目录：{currentPath}</span>
        <div className="settings-pan115-actions">
          <Button type="secondary" onClick={onCancel}>取消</Button>
          <Button
            type="primary"
            disabled={added || !page || listing.isError || listing.isFetching}
            onClick={() => onSelect({ id: location.id, path: currentPath })}
          >
            {added ? "该目录已添加" : "选择当前目录"}
          </Button>
        </div>
      </div>
    </div>
  );
}
