import Player from "xgplayer";
import "xgplayer/dist/index.min.css";
import { useEffect, useRef } from "react";

type MediaPlayerProps = {
  /** 可播放的预告片地址。 */
  url: string;
  /** 播放前展示的横向封面。 */
  poster?: string | null;
};

/** 公共西瓜播放器；切源或卸载时销毁实例，避免弹层反复打开后残留资源。 */
export function MediaPlayer({ url, poster }: MediaPlayerProps) {
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!containerRef.current || !url) return;
    const player = new Player({
      el: containerRef.current,
      url,
      poster: poster || undefined,
      autoplay: false,
      playsinline: true,
      width: "100%",
      height: "100%",
    });
    return () => player.destroy();
  }, [poster, url]);

  if (!url) return <div className="media-player-empty">暂无可播放的预告</div>;
  return <div className="media-player" ref={containerRef} aria-label="预告播放器" />;
}
