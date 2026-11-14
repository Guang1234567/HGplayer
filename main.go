// hgplayer 是红果短剧客户端的 Go / Wails 重构版本。
//
// 本文件是应用入口：初始化数据目录与日志器，组装应用服务，
// 把同源 HTTP 服务挂载到 AssetServer（/api、/media 由后端处理，
// 其余交给前端静态资源），创建主窗口并运行应用。
package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"log"
	"net/http"

	"github.com/wailsapp/wails/v3/pkg/application"

	"hgplayer/internal/apppath"
	"hgplayer/internal/device"
	"hgplayer/internal/logx"
	"hgplayer/internal/server"
	"hgplayer/internal/sign"
)

// Wails 使用 Go 的 embed 把前端产物嵌入二进制。
// frontend/dist 下的所有文件都会被嵌入并由 AssetServer 提供。
//
//go:embed all:frontend/dist
var frontendAssets embed.FS

// app 聚合应用运行期依赖，供 assetHandler 与各服务使用。
type app struct {
	dirs   apppath.Dirs
	logger *logx.Logger
	svc    *server.Server
}

func main() {
	a, err := newApp()
	if err != nil {
		log.Fatalf("初始化失败: %v", err)
	}

	w := application.New(application.Options{
		Name:        "hgplayer",
		Description: "红果短剧客户端（Go / Wails）",
		Assets: application.AssetOptions{
			// 同源路由：/api、/media 由后端服务处理，
			// 其余路径交由前端静态资源，保证前端与 API 同 origin。
			Handler: a.assetHandler(),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	// 主窗口：加载同源根路径。
	w.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:             "红果短剧",
		Width:             1280,
		Height:            800,
		MinWidth:          960,
		MinHeight:         640,
		Mac:               application.MacWindow{InvisibleTitleBarHeight: 50},
		BackgroundColour:  application.NewRGB(17, 19, 24),
		URL:               "/",
	})

	if err := w.Run(); err != nil {
		log.Fatal(err)
	}
}

// newApp 初始化数据目录、日志器与应用服务。
func newApp() (*app, error) {
	dirs, err := apppath.Resolve()
	if err != nil {
		return nil, err
	}
	if err := dirs.Ensure(); err != nil {
		return nil, err
	}

	logger := logx.New(logx.Options{
		Dir:       dirs.Logs,
		MaxSizeMB: 10,
		MaxFiles:  5,
		TailLines: 200,
	})

	svc := server.New(server.Config{
		Version:        version,
		SessionToken:   randomToken(),
		Logger:        logger,
		ArchiveDir:    dirs.Data,
		MediaProvider: localMediaProvider,
	}, sign.PlayerSigner{})

	// 预加载匿名设备身份（不存在则稍后由上游注册流程创建）。
	if info, exists, err := device.Load(dirs.Data); err != nil {
		logger.Warn("加载设备身份失败", "err", err)
	} else if exists {
		logger.Info("已加载本地设备身份", "device_id", info.DeviceID)
	}

	return &app{dirs: dirs, logger: logger, svc: svc}, nil
}

// assetHandler 组装前端静态资源 + 同源 /api、/media 的 Handler。
func (a *app) assetHandler() http.Handler {
	static := application.AssetFileServerFS(frontendAssets)

	mux := http.NewServeMux()
	mux.Handle("/api/", a.svc.Handler())
	mux.Handle("/media/", a.svc.Handler())
	mux.Handle("/", static)
	return mux
}

// randomToken 生成随机本地会话令牌。
func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// version 由构建期注入。
var version = "0.0.0-spike"
