package service

import (
	"log"
	"sync"
	"time"

	"taskbackend/internal/config"
)

// TaskNotice 一条待投递的外部通知。
type TaskNotice struct {
	Title   string
	Content string
	EmailTo string // 为空表示只推 Webhook
}

// Notifier 后台通知投递器。
//
// 邮件与 IM webhook 都是外部慢依赖，绝不能在处理 HTTP 请求或持有数据库写锁时同步调用，
// 否则一个卡住的 SMTP 服务器会把整站请求队列拖死。这里改为非阻塞入队 + 固定 worker 投递。
type Notifier struct {
	cfg   *config.Config
	queue chan TaskNotice
	wg    sync.WaitGroup

	// mu 同时保护 closed 标志与对 queue 的发送，保证不会向已关闭的通道发送
	mu       sync.Mutex
	closed   bool
	stopOnce sync.Once
}

// NewNotifier 创建通知投递器，需显式调用 Start。
func NewNotifier(cfg *config.Config) *Notifier {
	size := cfg.NotifyQueueSize
	if size < 1 {
		// 至少留一格缓冲：Enqueue 靠「发送不成功就丢弃」保证不阻塞，
		// 无缓冲通道会让这个语义形同虚设
		size = 1
	}
	return &Notifier{
		cfg:   cfg,
		queue: make(chan TaskNotice, size),
	}
}

// Start 拉起 worker；每个 worker 独立 recover，单个通知失败不影响后续。
func (n *Notifier) Start() {
	for i := 0; i < n.cfg.NotifyWorkers; i++ {
		n.wg.Add(1)
		go n.worker(i)
	}
	log.Printf("通知投递器已启动（worker=%d，队列=%d）", n.cfg.NotifyWorkers, cap(n.queue))
}

func (n *Notifier) worker(id int) {
	defer n.wg.Done()
	for notice := range n.queue {
		n.deliver(notice)
	}
	log.Printf("通知 worker %d 已退出", id)
}

func (n *Notifier) deliver(notice TaskNotice) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[notify] 投递 panic: %v", r)
		}
	}()
	if n.cfg.NotifyWebhookURL != "" {
		// SendWebhook 内部已记失败日志，webhook 侧多为推送幂等，不做重试
		_ = SendWebhook(n.cfg, notice.Title, notice.Content)
	}
	if notice.EmailTo != "" && n.cfg.SMTPConfigured() {
		if err := retry(2, 2*time.Second, func() error {
			return SendEmail(n.cfg, notice.EmailTo, notice.Title, notice.Content)
		}); err != nil {
			log.Printf("[notify] 邮件最终发送失败（收件人 %s）: %v", notice.EmailTo, err)
		}
	}
}

// Enqueue 非阻塞投递；队列满时丢弃并记日志，宁可丢通知也不拖慢请求。
func (n *Notifier) Enqueue(notice TaskNotice) {
	if notice.Title == "" && notice.Content == "" {
		return
	}
	// 必须持锁判断 closed 再发送：向已关闭的通道发送会 panic 打崩整个进程。
	// 靠 select 把 <-stopped 和发送并列写是不行的——两个分支同时就绪时 select 随机选路，
	// 停机窗口里每一次入队都是一次抛硬币。
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		log.Printf("[notify] 投递器已停止，丢弃通知：%s", notice.Title)
		return
	}
	select {
	case n.queue <- notice:
	default:
		log.Printf("[notify] 通知队列已满，丢弃：%s", notice.Title)
	}
}

// Stop 停止接收并等待队列排空，最多等待 timeout。
func (n *Notifier) Stop(timeout time.Duration) {
	n.stopOnce.Do(func() {
		// 持锁关闭：此刻不会有任何 Enqueue 正卡在发送上，关闭后也没人再发
		n.mu.Lock()
		n.closed = true
		close(n.queue)
		n.mu.Unlock()
	})

	done := make(chan struct{})
	go func() {
		n.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		log.Println("[notify] 等待通知队列排空超时，强制退出")
	}
}

// retry 最多执行 attempts+1 次，每次间隔 backoff 线性递增。
func retry(attempts int, backoff time.Duration, fn func() error) error {
	var err error
	for i := 0; i <= attempts; i++ {
		if err = fn(); err == nil {
			return nil
		}
		if i < attempts {
			time.Sleep(time.Duration(i+1) * backoff)
		}
	}
	return err
}
