// @vitest-environment node
import { readFileSync, readdirSync, existsSync } from "node:fs";
import { dirname, resolve, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, test } from "vitest";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../../..");
const src = resolve(root, "frontend/src");
const requiredPages = ["actors/ActorListPage.tsx", "tags/TagListPage.tsx", "films/AllFilmsPage.tsx", "downloads/DownloadListPage.tsx", "search/SearchPage.tsx", "rank/RankPage.tsx", "system/LogsPage.tsx"];

/** 仅遍历指定源码及文档目录，不扫描依赖、旧资料和运行数据。 */
function files(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = resolve(directory, entry.name);
    return entry.isDirectory() ? files(path) : [path];
  });
}

/** 检查项目约定的显式Arco声明；动态封装需扩展检查，不允许静默绕过。 */
function gridErrors(source: string): string[] {
  const errors: string[] = [];
  const text = source.replace(/\{\/\*[\s\S]*?\*\/\}/g, "");
  if (!/import\s*\{[^}]*\bGrid\b[^}]*\}\s*from\s*["']@arco-design\/web-react["']/.test(text)) errors.push("必须从Arco根包导入Grid");
  const rows = [...text.matchAll(/<Grid\.Row\b[^>]*>/g)];
  if (!rows.length) errors.push("缺少Grid.Row");
  for (const [tag] of rows) {
    if (!/gutter=\{\[12,\s*12\]\}/.test(tag) || !/justify=["']start["']/.test(tag) || !/align=["']center["']/.test(tag)) errors.push("Row间距或对齐错误");
    if (/\{\.\.\.|\b(?:style|className)=/.test(tag)) errors.push("Row不得覆盖布局");
  }
  const cols = [...text.matchAll(/<Grid\.Col\b([^>]*)>([\s\S]*?)<\/Grid\.Col>/g)];
  if (!cols.length) errors.push("缺少Grid.Col");
  for (const match of cols) {
    for (const prop of ["xs={24}", "sm={12}", "md={8}", "xl={4}"]) if (!match[1].includes(prop)) errors.push("Col缺少" + prop);
    if (/\{\.\.\.|\b(?:span|offset|push|pull|order|flex|lg|xxl|xxxl|style)=/.test(match[1])) errors.push("Col不得覆盖跨度或位置");
    if (/<Grid\.(?:Col|Row)\b/.test(match[2])) errors.push("不得嵌套筛选栅格");
    const rowStart = text.lastIndexOf("<Grid.Row", match.index);
    if (rowStart < 0 || text.lastIndexOf("</Grid.Row>", match.index) > rowStart) errors.push("Col必须在Row内");
  }
  for (const control of text.matchAll(/<(?:Input|Select|DatePicker\.RangePicker)\b/g)) {
    if (!cols.some((col) => control.index > col.index && control.index < col.index + col[0].length)) errors.push("筛选输入必须位于Grid.Col内");
  }
  const action = cols.findIndex((col) => /filter-actions/.test(col[2]));
  if (action >= 0 && cols.slice(action + 1).some((col) => /filter-(?:field|labeled)/.test(col[2]))) errors.push("按钮必须位于全部条件之后");
  if (/filter-grid|gridTemplateColumns|gridColumn|marginLeft\s*:\s*["']auto/.test(text)) errors.push("不得重做筛选分列或右推按钮");
  return errors;
}

/** 只约束筛选选择器，独立卡片、设置及弹窗布局不受六等份规则限制。 */
function filterCssErrors(css: string): string[] {
  const errors: string[] = [];
  for (const rule of css.replace(/\/\*[\s\S]*?\*\//g, "").matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    if (!/\.(?:filter-(?:grid|toolbar|field|labeled|actions|control)|download-filters|logs-filters|rank-filter-bar|tag-toolbar)\b/.test(rule[1])) continue;
    if (/grid-template-columns|grid-column|justify-content\s*:\s*space-between|margin-left\s*:\s*auto|!important/.test(rule[2])) errors.push(rule[1].trim());
  }
  return errors;
}

describe("列表筛选栅格规范", () => {
  const pages = files(resolve(src, "features")).filter((path) => path.endsWith("Page.tsx"));
  for (const path of pages) {
    const source = readFileSync(path, "utf8");
    const required = requiredPages.some((page) => path.replaceAll("\\", "/").endsWith(page));
    const filterPage = !/[/\\](?:SettingsPage|LoginPage)\.tsx$/.test(path) && /role=["']search["']|filter-toolbar|filter-(?:field|labeled)|<Grid\.Row/.test(source);
    if (required || filterPage) test(relative(src, path), () => expect(gridErrors(source)).toEqual([]));
  }
  test("七个既有筛选入口仍受检查", () => {
    for (const page of requiredPages) expect(existsSync(resolve(src, "features", page))).toBe(true);
  });
  test("公共及页面CSS不覆盖筛选栅格", () => {
    for (const path of files(src).filter((file) => file.endsWith(".css"))) expect(filterCssErrors(readFileSync(path, "utf8")), path).toEqual([]);
  });
  const valid = [
    'import { Grid } from "@arco-design/web-react";',
    '<Grid.Row gutter={[12, 12]} justify="start" align="center">',
    '<Grid.Col xs={24} sm={12} md={8} xl={4}><div className="filter-field"><Input /></div></Grid.Col>',
    '<Grid.Col xs={24} sm={12} md={8} xl={4}><div className="filter-actions"><Button /></div></Grid.Col>',
    '</Grid.Row>',
  ].join("\n");
  test("错误跨度、日期跨列、栅格外输入和右推样式会被拦截", () => {
    expect(gridErrors(valid)).toEqual([]);
    expect(gridErrors(valid.replaceAll("xl={4}", "xl={6}"))).not.toEqual([]);
    expect(gridErrors(valid.replace("xl={4}", "xl={8}"))).not.toEqual([]);
    expect(gridErrors(valid.replace("xl={4}", "xl={4} style={{ gridColumn: 2 }}"))).not.toEqual([]);
    expect(gridErrors(valid + "<Input />")).not.toEqual([]);
    expect(filterCssErrors(".filter-grid { grid-template-columns: repeat(4, 1fr); }")).not.toEqual([]);
    expect(filterCssErrors(".filter-actions { margin-left: auto; }")).not.toEqual([]);
  });
});

describe("规范入口完整性", () => {
  test("根入口小于20KiB且专项链接存在", () => {
    const entry = readFileSync(resolve(root, "AGENTS.md"), "utf8");
    expect(Buffer.byteLength(entry, "utf8")).toBeLessThan(20 * 1024);
    for (const role of ["workflow", "frontend", "backend", "database"]) expect(entry).toContain("docs/agents/" + role + "/AGENTS.md");
    for (const file of [resolve(root, "AGENTS.md"), ...files(resolve(root, "docs/agents"))]) {
      const text = readFileSync(file, "utf8");
      for (const link of text.matchAll(/\]\(([^)#]+)(?:#[^)]*)?\)/g)) {
        if (!/^(?:https?:|mailto:)/.test(link[1])) expect(existsSync(resolve(dirname(file), link[1])), file + ": " + link[1]).toBe(true);
      }
    }
  });
});
