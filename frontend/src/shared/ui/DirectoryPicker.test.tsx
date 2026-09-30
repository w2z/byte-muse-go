// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DirectoryPicker } from "./DirectoryPicker";

afterEach(cleanup);

/** 渲染根目录下含两个子目录的选择器；返回渲染结果与各回调，便于断言选中行为。 */
function renderPicker(overrides: Partial<Parameters<typeof DirectoryPicker>[0]> = {}) {
  const onConfirm = vi.fn();
  const onCancel = vi.fn();
  const onNavigate = vi.fn();
  const onEnter = vi.fn();
  const view = render(
    <DirectoryPicker
      crumbs={[{ key: "0", name: "根目录" }]}
      onNavigate={onNavigate}
      onEnter={onEnter}
      entries={[
        { key: "10", name: "电影" },
        { key: "11", name: "电视剧" },
      ]}
      status="ready"
      onConfirm={onConfirm}
      onCancel={onCancel}
      {...overrides}
    />,
  );
  return { ...view, onConfirm, onCancel, onNavigate, onEnter };
}

describe("公共目录选择组件", () => {
  it("未选中目录时确认按钮不可点，选中后确认回传选中目录", async () => {
    const user = userEvent.setup();
    const { onConfirm } = renderPicker();
    const confirm = screen.getByRole("button", { name: "确认" });
    expect(confirm).toBeDisabled();

    await user.click(screen.getByRole("button", { name: "电影" }));
    expect(confirm).toBeEnabled();
    await user.click(confirm);
    expect(onConfirm).toHaveBeenCalledWith({ key: "10", name: "电影" });
  });

  it("单击选中目录，双击进入下一级", async () => {
    const user = userEvent.setup();
    const { onEnter } = renderPicker();

    await user.click(screen.getByRole("button", { name: "电影" }));
    expect(screen.getByRole("button", { name: "电影" })).toHaveAttribute("aria-pressed", "true");

    await user.dblClick(screen.getByRole("button", { name: "电视剧" }));
    expect(onEnter).toHaveBeenCalledWith({ key: "11", name: "电视剧" });
  });

  it("点击末级蓝色路径选中当前目录，根目录同样可被选择", async () => {
    const user = userEvent.setup();
    const { onConfirm } = renderPicker();
    const confirm = screen.getByRole("button", { name: "确认" });

    await user.click(screen.getByRole("button", { name: "根目录" }));
    expect(confirm).toBeEnabled();
    await user.click(confirm);
    expect(onConfirm).toHaveBeenCalledWith({ key: "0", name: "根目录", disabled: false, path: "根目录" });
  });

  it("选中后底部展示选中路径，未选中时不显示", async () => {
    const user = userEvent.setup();
    renderPicker({
      crumbs: [{ key: "0", name: "根目录" }, { key: "10", name: "电影" }],
      entries: [{ key: "11", name: "动作片", path: "/电影/动作片" }],
      currentPath: "/电影",
    });

    expect(screen.queryByText(/当前选择/)).toBeNull();

    await user.click(screen.getByRole("button", { name: "动作片" }));
    expect(screen.getByText("当前选择: /电影/动作片")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "动作片" })).toHaveClass("directory-picker-entry-selected");

    await user.click(screen.getByRole("button", { name: "电影" }));
    expect(screen.getByText("当前选择: /电影")).toBeInTheDocument();
  });

  it("可在列表与宫格之间切换展示方式", async () => {
    const user = userEvent.setup();
    renderPicker();
    const list = screen.getByRole("radio", { name: "列表" });
    const grid = screen.getByRole("radio", { name: "宫格" });
    expect(list).toBeChecked();

    await user.click(grid);
    expect(grid).toBeChecked();
  });

  it("再次单击已选中的目录可取消选中，确认按钮随之不可点", async () => {
    const user = userEvent.setup();
    renderPicker();
    const confirm = screen.getByRole("button", { name: "确认" });
    const entry = screen.getByRole("button", { name: "电影" });

    await user.click(entry);
    expect(entry).toHaveAttribute("aria-pressed", "true");
    expect(confirm).toBeEnabled();

    await user.click(entry);
    expect(entry).toHaveAttribute("aria-pressed", "false");
    expect(confirm).toBeDisabled();
    expect(screen.queryByText(/当前选择/)).toBeNull();
  });

  it("已添加的目录不可再选，也不能作为确认结果", async () => {
    renderPicker({ entries: [{ key: "10", name: "电影", disabled: true }] });

    expect(screen.getByRole("button", { name: "电影" })).toBeDisabled();
    expect(screen.getByText("已添加")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "确认" })).toBeDisabled();
  });

  it("进入下一级后清空上一级的选中项", async () => {
    const user = userEvent.setup();
    const view = renderPicker();
    await user.click(screen.getByRole("button", { name: "电影" }));
    expect(screen.getByRole("button", { name: "确认" })).toBeEnabled();

    view.rerender(
      <DirectoryPicker
        crumbs={[{ key: "0", name: "根目录" }, { key: "10", name: "电影" }]}
        onNavigate={view.onNavigate}
        onEnter={view.onEnter}
        entries={[]}
        status="ready"
        onConfirm={view.onConfirm}
        onCancel={view.onCancel}
      />,
    );
    expect(screen.getByRole("button", { name: "确认" })).toBeDisabled();
  });
});
