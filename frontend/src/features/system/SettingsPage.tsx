import { Button, Form, Input, Message, Tabs } from "@arco-design/web-react";
import { useQuery } from "@tanstack/react-query";
import { useEffect } from "react";
import { apiRequest } from "../../shared/api/client";
import type { SystemSettings } from "../../shared/api/types";
import { PageState } from "../../shared/ui/PageState";

type SettingField = { label: string; key: string; secret?: boolean; value?: string };
type SettingGroup = { title: string; code: string; fields: SettingField[] };

const groups: SettingGroup[] = [
  { title: "基础运行环境", code: "RUNTIME", fields: [{ label: "数据库引擎", key: "database_driver" }, { label: "演示数据", key: "demo_seed_enabled" }] },
  { title: "资源源站", code: "SOURCES", fields: [{ label: "MTEAM API 密钥", key: "MTEAM_API_KEY", secret: true }, { label: "番号订阅源", key: "JAVDB_HOST" }] },
  { title: "媒体库", code: "LIBRARIES", fields: [{ label: "Emby 地址", key: "EMBY_URL" }, { label: "Emby API 密钥", key: "EMBY_API_KEY", secret: true }, { label: "Plex 地址 / Token", key: "PLEX_URL · PLEX_TOKEN · X-PLEX-TOKEN", secret: true }, { label: "Jellyfin 地址 / API 密钥", key: "JELLYFIN_URL · JELLYFIN_API_KEY", secret: true }] },
  { title: "通知与消息", code: "MESSAGING", fields: [{ label: "微信代理 / Token", key: "微信代理 · WECHAT_TOKEN", secret: true }, { label: "Telegram Bot Token", key: "TELEGRAM_BOT_TOKEN", secret: true }] },
  { title: "下载器", code: "DOWNLOADERS", fields: [{ label: "qBittorrent 地址 / 密码", key: "QBITTORRENT_URL · qbittorrent 密码", secret: true }, { label: "qBittorrent 下载地址 / 分类", key: "qbittorrent 下载地址 · qbittorrent 下载分类" }, { label: "Transmission 地址 / 密码", key: "TRANSMISSION_URL · Transmission 密码", secret: true }] },
  { title: "存储", code: "STORAGE", fields: [{ label: "Thunder / CloudNAS", key: "THUNDER_URL · CLOUDNAS_URL" }, { label: "CD2 密码 / 保存路径", key: "CD2 密码 · CD2 保存路径", secret: true }] },
  { title: "定时任务", code: "SCHEDULES", fields: [{ label: "榜单订阅定时任务", key: "榜单订阅定时任务 cron" }, { label: "演员订阅定时任务", key: "演员订阅定时任务 cron" }, { label: "标签订阅定时任务", key: "标签订阅定时任务 cron" }, { label: "番号订阅定时任务", key: "番号订阅定时任务 cron" }] },
  { title: "翻译与 AI", code: "AI", fields: [{ label: "百度翻译 API 密钥", key: "BAIDU_API_KEY", secret: true }, { label: "Google API 密钥", key: "GOOGLE_API_KEY", secret: true }, { label: "OpenAI 地址 / API 密钥", key: "OPENAI_URL · OPENAI_API_KEY", secret: true }] },
  { title: "网络与爬虫", code: "NETWORK", fields: [{ label: "代理地址", key: "代理地址" }, { label: "外网访问地址", key: "外网访问地址" }, { label: "绕过地址", key: "BYPASS_URL" }] },
];

export function SettingsPage() {
  const [form] = Form.useForm();
  const query = useQuery({ queryKey: ["system-settings"], queryFn: () => apiRequest<SystemSettings>("/system/settings") });
  useEffect(() => {
    if (query.data) {
      form.setFieldsValue({
        database_driver: query.data.database_driver,
        demo_seed_enabled: query.data.demo_seed_enabled ? "已启用" : "未启用",
      });
    }
  }, [form, query.data]);
  return (
    <section>
      <div className="page-heading"><div><div className="eyebrow">系统 / CONFIGURATION</div><h1>设置</h1><p className="page-description">按 NAS 版 ByteMuse 的分组组织设置。当前仅展示 Go 后端已提供的安全配置状态，未提供接口的字段保持禁用。</p></div><div className="settings-readonly-badge">只读配置</div></div>
      <PageState isLoading={query.isLoading} error={query.error} isEmpty={false} emptyText="" onRetry={() => void query.refetch()}>
        <div className="settings-form-shell panel">
          <Tabs defaultActiveTab="RUNTIME" className="settings-tabs">
            {groups.map((group) => (
              <Tabs.TabPane key={group.code} title={group.title}>
                <Form form={form} layout="vertical" className="settings-form" onSubmit={(values) => { void values; Message.info("当前版本只读，设置保存接口尚未开放"); }}>
                  <div className="settings-form-heading"><div><h2>{group.title}</h2><p>配置项按照后端能力展示，敏感值不会回显。</p></div><span>{group.code}</span></div>
                  <div className="settings-fields">
                    {group.fields.map((field) => {
                      const connected = field.key === "database_driver" || field.key === "demo_seed_enabled";
                      return <Form.Item key={field.key} label={field.label} field={field.key}><Input disabled placeholder={connected ? undefined : "后端接口未接入"} type={field.secret ? "password" : "text"} /></Form.Item>;
                    })}
                  </div>
                  <div className="settings-form-footer"><span>未接入的字段不会发送请求或写入配置。</span><Button type="primary" htmlType="submit" disabled>保存设置</Button></div>
                </Form>
              </Tabs.TabPane>
            ))}
          </Tabs>
        </div>
      </PageState>
    </section>
  );
}
