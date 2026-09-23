import { Button, Carousel, Checkbox, Form, Input } from "@arco-design/web-react";
import { IconCloud, IconFolder, IconLock, IconPlayArrowFill, IconUser, IconVideoCamera } from "@arco-design/web-react/icon";
import { useMutation } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { apiRequest } from "../../shared/api/client";
import type { LoginResponse } from "../../shared/api/types";
import { useSession } from "../../shared/auth/session";

type LoginFormValues = { username: string; password: string };

/** 上一版把账号与口令明文写在这个键里，现已废弃，仅用于清理遗留数据。 */
const LEGACY_LOGIN_PARAMS_KEY = "bytemuse:login-params";

/** 清理上一版遗留的明文凭据，避免旧口令继续留在浏览器存储中。 */
function clearLegacyLoginParams() {
  try {
    localStorage.removeItem(LEGACY_LOGIN_PARAMS_KEY);
  } catch {
    // 存储不可用时无需处理。
  }
}

/** 剔除输入中的空白字符，账号与口令中的空格都按误输入处理。 */
function stripSpaces(value: unknown) {
  return String(value ?? "").replace(/\s+/g, "");
}

/** 轮播配图：用页面内图形元素拼出的抽象示意图，避免引入外部图片资源。 */
function BannerArt({ variant }: { variant: number }) {
  return (
    <div className={`login-carousel-art login-art-v${variant}`} aria-hidden="true">
      <div className="login-art-window">
        <div className="login-art-bar"><span /><span /><span /></div>
        <div className="login-art-rows">
          <div className="login-art-row"><IconVideoCamera /><span className="login-art-line" /></div>
          <div className="login-art-row"><IconCloud /><span className="login-art-line login-art-line-short" /></div>
          <div className="login-art-row"><IconFolder /><span className="login-art-line" /></div>
        </div>
      </div>
      <div className="login-art-chip login-art-chip-a"><IconPlayArrowFill /></div>
      <div className="login-art-chip login-art-chip-b"><IconVideoCamera /></div>
    </div>
  );
}

/** 左侧轮播文案：参照页放的是三条产品卖点，这里换成 ByteMuse 自身能力。 */
const BANNER_SLIDES = [
  { title: "一站式媒体订阅与下载编排", subTitle: "从榜单、厂牌到演员，订阅规则集中维护" },
  { title: "多下载器与媒体库联动", subTitle: "qBittorrent、Transmission 与 Emby、Jellyfin 保持同一步调" },
  { title: "自托管，数据留在自己手里", subTitle: "SQLite、PostgreSQL、MySQL 单容器部署，不依赖外部服务" },
];

/**
 * 登录页参照 Arco Design Pro（react-pro.arco.design/login）：
 * 左侧深蓝渐变 banner + 三帧轮播，右侧居中表单，页脚贴底。
 *
 * “记住密码”只决定服务端会话的有效期：勾选 30 天，不勾选 1 天。账号与口令不落本地存储。
 *
 * 登录失败原因写在表单上方的固定占位行里，不用命令式弹层：当前 Arco React 版本
 * 依赖 react-dom 主入口的 createRoot/render，而 React 19 已不再从该入口导出二者，
 * 命令式弹层会静默渲染失败。
 */
export function LoginPage() {
  const navigate = useNavigate();
  const setUser = useSession((state) => state.setUser);
  const [form] = Form.useForm<LoginFormValues>();
  const [remember, setRemember] = useState(false);
  const [errorMessage, setErrorMessage] = useState("");
  useEffect(clearLegacyLoginParams, []);
  const login = useMutation({
    mutationFn: (values: LoginFormValues) =>
      apiRequest<LoginResponse>("/auth/login", {
        method: "POST",
        body: JSON.stringify({ ...values, remember }),
      }),
    onSuccess: (result) => {
      setUser(result.user);
      navigate("/dashboard");
    },
    onError: (error: Error) => {
      setErrorMessage(error.message);
    },
  });

  const submit = (values: LoginFormValues) => {
    // loading 期间按钮仍可点击，这里显式拦截重复提交。
    if (login.isPending) return;
    setErrorMessage("");
    login.mutate(values);
  };

  return (
    <main className="login-shell">
      <div className="login-logo">
        <span className="login-logo-mark">BM</span>
        <div className="login-logo-text">ByteMuse</div>
      </div>
      <div className="login-banner">
        <div className="login-banner-inner">
          <Carousel className="login-carousel" animation="fade">
            {BANNER_SLIDES.map((slide, index) => (
              <div key={slide.title}>
                <div className="login-carousel-item">
                  <div className="login-carousel-title">{slide.title}</div>
                  <div className="login-carousel-sub-title">{slide.subTitle}</div>
                  <BannerArt variant={index + 1} />
                </div>
              </div>
            ))}
          </Carousel>
        </div>
      </div>
      <div className="login-content">
        <div className="login-form-wrapper">
          <h1 className="login-form-title">登录 ByteMuse</h1>
          <div className="login-form-sub-title">登录您的 ByteMuse 管理账号</div>
          <div className="login-form-error-msg">{errorMessage}</div>
          <Form form={form} layout="vertical" onSubmit={submit}>
            <Form.Item
              field="username"
              normalize={stripSpaces}
              rules={[{ required: true, message: "用户名不能为空" }]}
            >
              <Input
                prefix={<IconUser />}
                placeholder="请输入用户名"
                autoComplete="username"
                onPressEnter={() => form.submit()}
              />
            </Form.Item>
            <Form.Item
              field="password"
              normalize={stripSpaces}
              rules={[{ required: true, message: "密码不能为空" }]}
            >
              <Input.Password
                prefix={<IconLock />}
                placeholder="请输入密码"
                autoComplete="current-password"
                onPressEnter={() => form.submit()}
              />
            </Form.Item>
            <div className="login-form-actions">
              <div className="login-form-password-actions">
                <Checkbox checked={remember} onChange={(checked) => setRemember(checked)}>
                  记住密码
                </Checkbox>
              </div>
              <Button type="primary" long htmlType="submit" loading={login.isPending}>
                登录
              </Button>
            </div>
          </Form>
        </div>
        <div className="login-footer">ByteMuse · 自托管 PT 订阅与媒体库编排</div>
      </div>
    </main>
  );
}
