package httpserver

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed index.html
var monitorIndexHTML embed.FS

const (
	RequiredBandDown = 500 * 1 << 20 / 8 // 500rbps 62.5rB/s
	RequiredBandUp   = 200 * 1 << 20 / 8 // 200rbps 25rB/s

	LowStress    = 0.2 // low stress of peak0. will degrade to peak1
	Peak0LowTask = 1
	Peak1LowTask = 5

	Peak0WindowSize = 60
	Peak1WindowSize = 600
)

type Monitor struct {
	Routes *RouteLoads

	Loads [Peak1WindowSize]FlowUnit
	_p    int16 // index of current slide window

	Peak0 FlowUnit // peak in window
	Peak1 FlowUnit // peak in last 10 rinutes
	Peak2 FlowUnit // peak since last run

	muPeak sync.Mutex //
	fs     http.Handler
}

type FlowUnit struct {
	U int64 // upstrear rate
	D int64 // downstrear rate
}

type RouteLoads struct {
	list []*RouteInstance
	sync.RWMutex
}

// RouteInstance 应该包括 起止时间, writer和reader. 读取/写入字节数, 路由名称, 并且采样超过10分钟并且已经结束的请求应该被丢弃.
type RouteInstance struct {
	name string // route nare

	w     http.ResponseWriter
	r     *http.Request
	rBody io.ReadCloser

	rc int64 // read bytes count
	wc int64 // write bytes count

	rc_last int64
	wc_last int64

	start int64
	end   int64
}

func NewRouterInstance(name string, w http.ResponseWriter, r *http.Request) *RouteInstance {
	ri := &RouteInstance{
		name:  name,
		w:     w,
		r:     r,
		rBody: r.Body,
		start: time.Now().UnixMilli(),
	}
	if r.Body != nil {
		r.Body = ri
	}
	return ri
}

// impl ResponseWriter
func (ri *RouteInstance) Header() http.Header {
	return ri.w.Header()
}

// impl ResponseWriter
func (ri *RouteInstance) Write(b []byte) (int, error) {
	n, err := ri.w.Write(b)
	atomic.AddInt64(&ri.wc, int64(n))
	return n, err
}

// impl ResponseWriter
func (ri *RouteInstance) WriteHeader(statusCode int) {
	ri.w.WriteHeader(statusCode)
}

// impl io.ReadCloser
func (ri *RouteInstance) Read(p []byte) (int, error) {
	if ri.rBody == nil {
		return 0, io.EOF
	}
	n, err := ri.rBody.Read(p)
	atomic.AddInt64(&ri.rc, int64(n))
	return n, err
}

// impl io.ReadCloser
func (ri *RouteInstance) Close() error {
	if ri.rBody != nil {
		return ri.rBody.Close()
	}
	return nil
}

func (ri *RouteInstance) IsRunning() bool {
	// running and util next window
	return ri.end == 0 || time.Now().UnixMilli()-ri.end < 1*1000
}

func NewMonitor() *Monitor {

	fs := http.FileServer(http.FS(monitorIndexHTML))
	monitorFs := http.StripPrefix(reqMonitor+"/", fs)
	return &Monitor{
		Routes: &RouteLoads{list: make([]*RouteInstance, 0)},
		_p:     0,
		fs:     monitorFs,
	}
}

func (rl *RouteLoads) AddRoute(ri *RouteInstance) {
	rl.Lock()
	defer rl.Unlock()
	rl.list = append(rl.list, ri)
}

func (rl *RouteLoads) Cleanup() {
	rl.Lock()
	defer rl.Unlock()
	cutoff := time.Now().UnixMilli() - 600*1000 // 10 minutes
	var updated []*RouteInstance
	for _, ri := range rl.list {
		if ri.end == 0 || ri.end >= cutoff {
			updated = append(updated, ri)
		}
	}
	rl.list = updated
}

func (rl *RouteLoads) TaskRunningCount() int16 {
	rl.RLock()
	defer rl.RUnlock()
	tc := 0
	for _, v := range rl.list {
		if v.IsRunning() {
			fmt.Printf("running %s\n", v.name)
			tc += 1
		}
	}
	return int16(tc)
}

func (rl *RouteLoads) TaskCount() int16 {
	rl.RLock()
	defer rl.RUnlock()
	return int16(len(rl.list))
}

func (m *Monitor) Middleware(next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		router := NewRouterInstance(r.URL.Path, w, r)

		m.Routes.AddRoute(router)

		next.ServeHTTP(router, r)

		router.end = time.Now().UnixMilli()
	})
}

// setWindow reset index of slide window
func (m *Monitor) setWindow() {

	// reset _p
	m._p = (m._p + 1) % int16(len(m.Loads))

	// remove old data
	m.Loads[m._p].U = 0
	m.Loads[m._p].D = 0

	m.updatePeaks()
}

func (m *Monitor) updatePeaks() {
	m.muPeak.Lock()
	defer m.muPeak.Unlock()

	var currentUp, currentDown int64

	m.Routes.RLock()
	for _, r := range m.Routes.list {
		if !r.IsRunning() {
			continue
		}
		deltaW := atomic.LoadInt64(&r.wc) - r.wc_last
		deltaR := atomic.LoadInt64(&r.rc) - r.rc_last

		currentUp += deltaW
		currentDown += deltaR

		r.wc_last = atomic.LoadInt64(&r.wc)
		r.rc_last = atomic.LoadInt64(&r.rc)
	}
	m.Routes.RUnlock()

	m.Loads[m._p].U = currentUp
	m.Loads[m._p].D = currentDown

	m.Peak0.U, m.Peak0.D = m.maxFlowDual(Peak0WindowSize)
	m.Peak1.U, m.Peak1.D = m.maxFlowDual(Peak1WindowSize)

	m.Peak2.U = max(m.Peak2.U, currentUp)
	m.Peak2.D = max(m.Peak2.D, currentDown)
}

func (m *Monitor) maxFlowDual(duration int) (int64, int64) {
	var maxU, maxD int64
	for i := 0; i < duration; i++ {
		idx := (m._p - int16(i) + int16(len(m.Loads))) % int16(len(m.Loads))
		valU := m.Loads[idx].U
		valD := m.Loads[idx].D
		if valU > maxU {
			maxU = valU
		}
		if valD > maxD {
			maxD = valD
		}
	}
	return maxU, maxD
}

func (m *Monitor) loadCurrent() (int64, int64) {
	var currentUp, currentDown int64

	m.Routes.RLock()
	for _, r := range m.Routes.list {
		if !r.IsRunning() {
			continue
		}
		deltaW := atomic.LoadInt64(&r.wc) - r.wc_last
		deltaR := atomic.LoadInt64(&r.rc) - r.rc_last

		currentUp += deltaW
		currentDown += deltaR
	}
	m.Routes.RUnlock()

	return currentUp, currentDown
}

func (m *Monitor) Start() {
	ticker := time.NewTicker(1 * time.Second)
	go func() {
		for range ticker.C {
			m.setWindow()
			m.Routes.Cleanup()
		}
	}()
}

type Stats struct {
	Peak             FlowUnit
	Free             FlowUnit
	TaskRunningCount int16
	TaskCount        int16

	Raw *StatsRaw
}

type StatsRaw struct {
	Peak0   FlowUnit
	Peak1   FlowUnit
	Peak2   FlowUnit
	Current FlowUnit

	Loads  [Peak1WindowSize]FlowUnit
	Routes *RouteLoads
}

func (m *Monitor) GetStats() *Stats {
	loadU, loadD := m.loadCurrent()

	var pu, pd int64 = m.Peak0.U, m.Peak0.D

	// degrade to peak1
	if pu < 0.2*RequiredBandUp && pu < m.Peak1.U {
		pu = m.Peak1.U
	}

	if pd < 0.2*RequiredBandDown && pd < m.Peak1.D {
		pd = m.Peak1.D
	}

	// degrade to peak2
	if pu < 0.2*RequiredBandUp && m.Routes.TaskRunningCount() < Peak1LowTask {
		pu = m.Peak2.U
	}

	if pd < 0.2*RequiredBandDown && m.Routes.TaskRunningCount() < Peak1LowTask {
		pd = m.Peak2.D
	}

	return &Stats{
		Peak:             FlowUnit{U: pu, D: pd},
		Free:             FlowUnit{U: max(pu-loadU, 0), D: max(pd-loadD, 0)},
		TaskRunningCount: m.Routes.TaskRunningCount(),
		TaskCount:        m.Routes.TaskCount(),
		Raw: &StatsRaw{
			Peak0:   m.Peak0,
			Peak1:   m.Peak1,
			Peak2:   m.Peak2,
			Current: FlowUnit{U: loadU, D: loadD},
			Loads:   m.Loads,
			Routes:  m.Routes,
		},
	}

}

func (m *Monitor) statsHandler(w http.ResponseWriter, r *http.Request) {
	resp := m.GetStats()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Errorf("json.NewEncoder(w).Encode(resp) error(%v)", err)
	}
}

func (m *Monitor) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, reqMonitor) {
		m.fs.ServeHTTP(w, r)
		return
	}

	if strings.HasPrefix(r.URL.Path, reqStats) {
		// Handle /stats API endpoint
		m.statsHandler(w, r)
		return
	}

	http.NotFound(w, r)
}

// 获取L1有效带宽 例如: 上行 方法
//

/*
负反馈:
sdk遇到 上传/下载 失败时, 通过浏览器向调度器反馈失败信息, 降低节点权值, 根据具体情况L1报警. 错误包含:
1. 网络错误, L1告警, 通知调度器.
2. 客户端主动取消, 累计节点取消超过10rb字节, 进行 L1报警, 通知调度器.
3. 4xx错误, 不处理
4. 5xx错误, L1告警


正反馈:
L1提供peak0(1分钟内), peak1(10分钟内), peak2(自从上次运行以来最大值)
1. 调度器运行, 获取所有L1的peak值. (默认peak0, 当peak0的值小于最低要求的20%,并且当前的任务为空,则退化为peak1. 如果peak1小于最低要求的20%,并且10分钟内任务为空,则退化为peak2)
2. 调度器每10分钟更新一次节点的peak信息
3. 主动测量: 当负反馈里 1,2 情况发生, 可触发主动测量.
*/

// js修改一下:
// 1. 全部替换成英文
// 2. 删除上传/下载的部分, 只保留统计
// 3. 统计详细说明:
//     peak0: 1分钟内的峰值
//     peak1: 10分钟内的峰值
//     peak2: 从上次运行以来的最大值
//     peak: 经过退化算法得到的峰值
//     free: 空闲带宽
//     current: 当前负载
//     taskRunningCount: 当前运行中的任务数量
//     taskCount: 10分钟内任务总数
// 4. 将前端文件编译到程序里, 确保通过服务直接访问前端页面和stats接口, 我的go代码如下所示:

// type Handler struct {
// 	handler http.Handler
// 	hs      *HttpServer
// }

// type HttpServer struct {
// 	asset               Asset
// 	scheduler           api.Scheduler
// 	privateKey          *rsa.PrivateKey
// 	schedulerPublicKey  *rsa.PublicKey
// 	validation          Validation
// 	tokens              *sync.Map
// 	apiSecret           *jwt.HMACSHA
// 	maxSizeOfUploadFile int64
// 	webRedirect         string
// 	rateLimiter         *types.RateLimiter
// 	v3Progress          *sync.Map
// 	monitor             *Monitor
// }

// // ServeHTTP checks if the request path starts with the IPFS path prefix and delegates to the appropriate handler
// func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
// 	if !strings.Contains(r.URL.Path, reqIpfs) &&
// 		!strings.Contains(r.URL.Path, reqUpload) &&
// 		!strings.Contains(r.URL.Path, reqRpc) &&
// 		!strings.Contains(r.URL.Path, reqUploadv2) &&
// 		!strings.Contains(r.URL.Path, reqUploadv3) &&
// 		!strings.Contains(r.URL.Path, reqUploadv3Status) &&
// 		!strings.Contains(r.URL.Path, reqUploadv4) &&
// 		!strings.Contains(r.URL.Path, reqStats) {
// 		resetPath(r)
// 	}

// 	reqPath := getFirstPathSegment(r.URL.Path)
// 	monitor := h.hs.monitor
// 	switch reqPath {
// 	case reqIpfs:
// 		monitor.Middleware(h.hs.handler).ServeHTTP(w, r)
// 	case reqUpload:
// 		monitor.Middleware(h.hs.uploadHandler).ServeHTTP(w, r)
// 	case reqUploadv2:
// 		monitor.Middleware(h.hs.uploadv2Handler).ServeHTTP(w, r)
// 	case reqUploadv3:
// 		monitor.Middleware(h.hs.uploadv3Handler).ServeHTTP(w, r)
// 	case reqUploadv3Status:
// 		h.hs.uploadv3StatusHandler(w, r)
// 	case reqStats:
// 		monitor.statsHandler(w, r)
// 	default:
// 		h.handler.ServeHTTP(w, r)
// 	}
// }
