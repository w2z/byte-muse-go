import { Button } from "@arco-design/web-react";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { apiRequest } from "../../shared/api/client";
import { DirectoryPicker } from "../../shared/ui/DirectoryPicker";

/** 115 目录条目；只有 is_directory 为 true 的条目可以进入下一级。 */
export type Pan115FileEntry = {
  id: string;
  parent_id: string;
  name: string;
  is_directory: boolean;
  size: number;
  pick_code: string;
};

/** 一页 115 目录内容；path 是从根目录到当前目录的完整路径。 */
export type Pan115FilePage = {
  directory_id: string;
  path: { id: string; name: string }[];
  files: Pan115FileEntry[];
  total: number;
  has_more: boolean;
};

/** 115 用 "0" 表示网盘根目录。 */
export const pan115RootDirectoryID = "0";
/** 目录分页大小；后端默认 100、上限 1000，选择目录固定使用默认值。 */
const pickerPageSize = 100;

/** 把 115 返回的目录路径拼成展示用绝对路径；根目录为 "/"。 */
export function pan115PathLabel(path: { name: string }[]): string {
  const names = path.map((item) => item.name.trim()).filter((name) => name !== "");
  return names.length === 0 ? "/" : `/${names.join("/")}`;
}

/** 子目录的展示路径；父路径为根目录时直接拼在 "/" 之下。 */
function pan115ChildPath(parent: string, name: string): string {
  return parent === "/" ? `/${name}` : `${parent}/${name}`;
}

/**
 * 115 目录选择器：把 115 的目录接口适配到公共目录选择组件。
 *
 * 只列出文件夹，文件条目不可进入；未绑定账号时后端返回 409，这里直接展示后端文案。
 * 选择结果只有目录 ID 与展示路径，是否允许重复由调用方通过 selectedIDs 决定：
 * 已在 selectedIDs 中的目录标记为已添加，调用方也可以借此排除正在编辑的条目。
 */
export function Pan115DirectoryPicker({
  selectedIDs,
  onCancel,
  onSelect,
}: {
  selectedIDs: string[];
  onCancel: () => void;
  onSelect: (path: { id: string; path: string }) => void;
}) {
  const [location, setLocation] = useState({ id: pan115RootDirectoryID, page: 1 });
  const listing = useQuery({
    queryKey: ["pan115-files", location.id, location.page],
    queryFn: () =>
      apiRequest<Pan115FilePage>(
        `/pan115/files?directory_id=${encodeURIComponent(location.id)}&offset=${(location.page - 1) * pickerPageSize}&limit=${pickerPageSize}`,
      ),
    retry: false,
  });

  const page = listing.data;
  const currentPath = page ? pan115PathLabel(page.path) : "/";
  const directories = page?.files.filter((file) => file.is_directory) ?? [];

  return (
    <DirectoryPicker
      crumbs={(page?.path ?? []).map((item) => ({ key: item.id, name: item.name.trim() === "" ? "根目录" : item.name }))}
      onNavigate={(crumb) => setLocation({ id: crumb.key, page: 1 })}
      onEnter={(entry) => setLocation({ id: entry.key, page: 1 })}
      entries={directories.map((item) => ({
        key: item.id,
        name: item.name,
        // 子目录只有名称没有路径，用当前目录路径拼出展示路径，选中与确认都用这一份。
        path: pan115ChildPath(currentPath, item.name),
        disabled: selectedIDs.includes(item.id),
      }))}
      status={listing.isLoading ? "loading" : listing.isError ? "error" : "ready"}
      errorText={listing.error?.message}
      loadingText="正在读取 115 目录…"
      emptyText="当前目录下没有子文件夹"
      currentOccupied={selectedIDs.includes(location.id)}
      currentPath={currentPath}
      onConfirm={(entry) => onSelect({ id: entry.key, path: entry.path ?? currentPath })}
      onCancel={onCancel}
      footer={
        <div className="directory-picker-pagination">
          <span className="settings-field-description">
            第 {location.page} 页{page ? ` · 共 ${page.total} 项` : ""}
          </span>
          <div className="settings-pan115-actions">
            <Button
              type="secondary"
              disabled={location.page === 1 || listing.isFetching}
              onClick={() => setLocation({ id: location.id, page: location.page - 1 })}
            >
              上一页
            </Button>
            <Button
              type="secondary"
              disabled={!page?.has_more || listing.isFetching}
              onClick={() => setLocation({ id: location.id, page: location.page + 1 })}
            >
              下一页
            </Button>
          </div>
        </div>
      }
    />
  );
}
