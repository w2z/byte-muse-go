import { Button, Input, Modal } from "@arco-design/web-react";
import { useState } from "react";
import { ScanProgressDisplay, useScanProgress } from "./ScanProgress";
import { Pan115DirectoryPicker } from "./Pan115DirectoryPicker";

/** 一个扫描目录；id 是 115 目录 ID，path 是展示用的完整路径。 */
export type Pan115ScanPath = { id: string; path: string };

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
      {value.length === 0 ? null : (
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
        className="settings-directory-picker-modal"
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

/** 一次「扫描入库」的汇总结果，字段与后端 domain.Pan115LibraryScanResult 一一对应。 */
type Pan115LibraryScanResult = {
  directories: {
    id: string;
    path: string;
    files: number;
    matched: number;
    created: number;
    skipped: number;
    message: string;
  }[];
  files: number;
  matched: number;
  created: number;
  skipped: number;
};

/**
 * 115 扫描入库操作。
 *
 * 与扫描目录字段分开渲染，放在「115网盘」分组字段之后，形成「选择扫描目录 → 保存设置 → 扫描入库」的顺序。
 * 扫描始终使用已保存的扫描目录，草稿改动需先保存设置；草稿为空时禁用，避免提交必然失败的请求。
 */
export function Pan115LibraryScanAction({ value }: { value: Pan115ScanPath[] }) {
  const { scan, progress } = useScanProgress<Pan115LibraryScanResult>("/pan115/library/scan");
  const failedDirectories = scan.data?.directories.filter((item) => item.message !== "") ?? [];

  return (
    <div className="settings-strm-generate">
      <div className="settings-strm-scan">
        <Button
          type="secondary"
          loading={scan.isPending}
          disabled={scan.isPending || value.length === 0}
          onClick={() => scan.mutate()}
        >
          扫描入库
        </Button>
        <span className="settings-field-description">
          扫描使用已保存的扫描目录；修改后请先保存设置再扫描。
        </span>
      </div>
      <ScanProgressDisplay label="扫描" progress={progress} pending={scan.isPending} error={scan.isError} warning={failedDirectories.length > 0} />
      {scan.isError ? (
        <span className="settings-field-description">扫描失败：{scan.error.message}</span>
      ) : scan.data ? (
        <span className="settings-field-description">
          共 {scan.data.files} 个视频文件，识别 {scan.data.matched} 个番号，新增 {scan.data.created} 部影片，跳过 {scan.data.skipped} 个。
          {failedDirectories.map((item) => ` ${item.path}：${item.message}`).join("")}
        </span>
      ) : null}
    </div>
  );
}
