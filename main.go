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
	"net"
	"net/http"
	"os"

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

	// 开发模式下（wails3 dev 会设置 FRONTEND_DEVSERVER_URL）为 WebView2 打开
	// 远程调试端口，便于在 IDE 中附加调试前端代码。生产构建不设置该变量，
	// 因此发布版本不会带调试端口。
	var browserArgs []string
	if os.Getenv("FRONTEND_DEVSERVER_URL") != "" {
		browserArgs = []string{"--remote-debugging-port=9222"}
	}

	// 除桌面窗口外，再开放一个同源 HTTP 服务，便于用系统浏览器（含局域网内
	// 其它设备）访问同一套前端与 /api、/media 后端。默认监听所有网卡
	// （0.0.0.0:7788），启动时打印本机与局域网访问地址；可用 HGPLAYER_WEB_ADDR
	// 覆盖（如需仅本机访问，设为 127.0.0.1:7788），设为 "-" 则关闭。
	webAddr := os.Getenv("HGPLAYER_WEB_ADDR")
	if webAddr == "" {
		webAddr = "0.0.0.0:7788"
	}
	if webAddr != "-" {
		go serveLoopback(webAddr, a.assetHandler())
	}

	w := application.New(application.Options{
		Name:        "hgplayer",
		Description: "红果短剧客户端（Go / Wails）",
		Assets: application.AssetOptions{
			// 同源路由：/api、/media 由后端服务处理，
			// 其余路径交由前端静态资源，保证前端与 API 同 origin。
			Handler: a.assetHandler(),
		},
		Windows: application.WindowsOptions{
			AdditionalBrowserArgs: browserArgs,
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

// serveLoopback 以标准 HTTP 服务承载与桌面窗口相同的同源 handler，
// 使前端也能用系统浏览器（含局域网内其它设备）访问；/api、/media 复用同一后端代码。
func serveLoopback(addr string, handler http.Handler) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("浏览器访问服务启动失败（%s）: %v", addr, err)
		return
	}
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		port = "7788"
	}
	log.Printf("本机访问: http://127.0.0.1:%s", port)
	// 优先只列出默认出站网卡地址（局域网内其它设备最可能访问到的那个）；
	// 取不到时回退到枚举全部网卡。
	// 用 HGPLAYER_WEB_PROBE 指定「访问方所处网段」的探针地址：
	// 例如客户端在公司 10.x 网段时设为 10.1.161.1（如 10.1.161.100 对应 10.1.161.1），
	// 即可打印出通往 10.x 的网卡地址；不设则探默认出站网卡。
	if ip := primaryIP(os.Getenv("HGPLAYER_WEB_PROBE")); ip != "" {
		log.Printf("局域网访问: http://%s:%s", ip, port)
	} else {
		for _, ip := range lanAddrs() {
			log.Printf("局域网访问(候选): http://%s:%s", ip, port)
		}
	}
	srv := &http.Server{Handler: handler}
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Printf("浏览器访问服务退出: %v", err)
	}
}

// primaryIP 返回本机朝向 probe 目标所用路由的 IPv4 —— 也就是「打算从该网段访问的
// 设备」会看到的源地址。UDP 无连接，仅触发一次本机路由查表，不会真正发包。
//
// probe 为空时用 RFC 5737 文档地址 192.0.2.1（探默认出站网卡）；
// 传 10.1.161.1 这类目标则返回通往 10.x 网段的网卡地址（如 10.1.161.100）。
func primaryIP(probe string) string {
	if probe == "" {
		probe = "192.0.2.1:9"
	} else if _, _, err := net.SplitHostPort(probe); err != nil {
		probe = net.JoinHostPort(probe, "9")
	}
	log.Printf("探针地址: %s", probe)
	conn, err := net.Dial("udp4", probe)
	if err != nil {
		return ""
	}
	defer conn.Close()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		if ip := addr.IP.To4(); ip != nil && !ip.IsLoopback() {
			return ip.String()
		}
	}
	return ""
}

// lanAddrs 返回本机可用于局域网访问的 IPv4 地址（跳过未启用与回环网卡）。
func lanAddrs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil || ip.IsLoopback() {
				continue
			}
			out = append(out, ip.String())
		}
	}
	return out
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
	// registerDevice 注册一台新设备并替换当前身份（同时持久化）。
	registerDevice := func(ctx context.Context) (hgproto.Device, error) {
		fresh, err := proto.RegisterDevice(ctx)
		if err != nil {
			return hgproto.Device{}, err
		}
		if saveErr := device.Save(dirs.Data, deviceInfoOf(fresh)); saveErr != nil {
			logger.Warn("保存新设备身份失败", "err", saveErr)
		}
		proto.SetDevice(fresh)
		logger.Info("已注册新的匿名设备", "device_id", fresh.DeviceID)
		return fresh, nil
	}
	proto = hgproto.NewClient(signer, deviceOf(info),
		// 取址被平台按设备限流时注册一台新设备，并持久化。
		hgproto.WithDeviceRegistrar(registerDevice),
	)

	svc := server.New(server.Config{
		Version:      version,
		SessionToken: randomToken(),
		Logger:       logger,
		ArchiveDir:   dirs.Data,
		Proto:        proto,
		MediaProxy: media.NewProxy(streamResolver{proto: proto},
			media.WithLogger(logger),
			// 取址失败（200 空响应）时换设备重取一次，避免整次播放 502。
			media.WithRefresher(func(ctx context.Context) error {
				_, err := registerDevice(ctx)
				return err
			}),
		),
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
