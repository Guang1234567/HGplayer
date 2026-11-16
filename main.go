// hgplayer 是红果短剧客户端的 Go / Wails 重构版本。
//
// 本文件是应用入口：初始化数据目录与日志器，组装应用服务，
// 把同源 HTTP 服务挂载到 AssetServer（/api、/media 由后端处理，
// 其余交给前端静态资源），创建主窗口并运行应用。
package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"log"
	"net/http"

	"github.com/wailsapp/wails/v3/pkg/application"

	"hgplayer/internal/apppath"
	"hgplayer/internal/device"
	"hgplayer/internal/hgproto"
	"hgplayer/internal/logx"
	"hgplayer/internal/media"
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
		Title:            "红果短剧",
		Width:            1280,
		Height:           800,
		MinWidth:         960,
		MinHeight:        640,
		Mac:              application.MacWindow{InvisibleTitleBarHeight: 50},
		BackgroundColour: application.NewRGB(17, 19, 24),
		URL:              "/",
	})

	if err := w.Run(); err != nil {
		log.Fatal(err)
	}
}

// newApp 初始化数据目录、设备身份、协议客户端与应用服务。
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

	// 加载或生成匿名设备身份。
	info, exists, err := device.Load(dirs.Data)
	if err != nil {
		logger.Warn("加载设备身份失败", "err", err)
	}
	if !exists {
		info = device.Generate()
		if err := device.Save(dirs.Data, info); err != nil {
			logger.Warn("保存设备身份失败", "err", err)
		}
		logger.Info("已生成匿名设备身份", "device_id", info.DeviceID)
	} else {
		logger.Info("已加载本地设备身份", "device_id", info.DeviceID)
	}

	signer := sign.PlayerSigner{}
	var proto *hgproto.Client
	proto = hgproto.NewClient(signer, deviceOf(info),
		// 取址被平台按设备限流时注册一台新设备，并持久化。
		hgproto.WithDeviceRegistrar(func(ctx context.Context) (hgproto.Device, error) {
			fresh, err := proto.RegisterDevice(ctx)
			if err != nil {
				return hgproto.Device{}, err
			}
			if saveErr := device.Save(dirs.Data, deviceInfoOf(fresh)); saveErr != nil {
				logger.Warn("保存新设备身份失败", "err", saveErr)
			}
			logger.Info("已注册新的匿名设备", "device_id", fresh.DeviceID)
			return fresh, nil
		}),
	)

	svc := server.New(server.Config{
		Version:      version,
		SessionToken: randomToken(),
		Logger:       logger,
		ArchiveDir:   dirs.Data,
		Proto:        proto,
		MediaProxy:   media.NewProxy(streamResolver{proto: proto}, media.WithLogger(logger)),
	}, signer)

	return &app{dirs: dirs, logger: logger, svc: svc}, nil
}

// streamResolver 把协议层取址适配为媒体代理需要的解析器。
type streamResolver struct {
	proto *hgproto.Client
}

// ResolveStream 解析分集标识到片源。
func (s streamResolver) ResolveStream(ctx context.Context, vid string) (media.StreamSource, error) {
	info, err := s.proto.Play(ctx, vid)
	if err != nil {
		return media.StreamSource{}, err
	}
	return media.StreamSource{
		URL:          info.URL,
		Alternatives: backupURLs(info),
		Encrypted:    info.Encrypted,
		ContentKey:   info.ContentKey,
	}, nil
}

// backupURLs 收集备用片源地址（同一集的其它 CDN 节点）。
func backupURLs(info *hgproto.PlayInfo) []string {
	if info.BackupURL == "" || info.BackupURL == info.URL {
		return nil
	}
	return []string{info.BackupURL}
}

// assetHandler 组装前端静态资源 + 同源 /api、/media 的 Handler。
//
// 壳页面（/ 与 /index.html）由静态资源处理器直接返回，不经过后端中间件，
// 因此在服务壳页面时显式下发本地会话 Cookie。
func (a *app) assetHandler() http.Handler {
	static := application.AssetFileServerFS(frontendAssets)

	shell := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			a.svc.SetSessionCookie(w)
		}
		static.ServeHTTP(w, r)
	})

	mux := http.NewServeMux()
	mux.Handle("/api/", a.svc.Handler())
	mux.Handle("/media/", a.svc.Handler())
	mux.Handle("/", shell)
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
var version = "0.1.0"

// deviceOf 把持久化的设备身份映射为协议层设备。
//
// 机型档案为空时由协议层套用默认档案；注册过的设备带自己的档案。
func deviceOf(info device.Info) hgproto.Device {
	return hgproto.Device{
		DeviceID:        info.DeviceID,
		IID:             info.IID,
		CDID:            info.CDID,
		OpenUDID:        info.OpenUDID,
		TTReq:           info.TTReq,
		StoreRegion:     info.StoreRegion,
		StoreRegionSrc:  info.StoreRegionSrc,
		Model:           info.Model,
		Brand:           info.Brand,
		OSVersion:       info.OSVersion,
		OSAPI:           info.OSAPI,
		Resolution:      info.Resolution,
		DPI:             info.DPI,
		Channel:         info.Channel,
		Build:           info.Build,
		VersionCode:     "74332",
		VersionName:     "7.4.3.32",
		ManifestVersion: "74332",
		UpdateVersion:   "74332",
		PVPlayer:        "74332",
	}
}

// deviceInfoOf 把协议层设备映射回持久化结构。
func deviceInfoOf(dev hgproto.Device) device.Info {
	return device.Info{
		DeviceID:       dev.DeviceID,
		IID:            dev.IID,
		CDID:           dev.CDID,
		OpenUDID:       dev.OpenUDID,
		TTReq:          dev.TTReq,
		StoreRegion:    dev.StoreRegion,
		StoreRegionSrc: dev.StoreRegionSrc,
		Model:          dev.Model,
		Brand:          dev.Brand,
		OSVersion:      dev.OSVersion,
		OSAPI:          dev.OSAPI,
		Resolution:     dev.Resolution,
		DPI:            dev.DPI,
		Channel:        dev.Channel,
		Build:          dev.Build,
		VersionCode:    dev.VersionCode,
		VersionName:    dev.VersionName,
	}
}
