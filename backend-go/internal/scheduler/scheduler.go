package scheduler

import (
	"log"
	"time"

	"github.com/robfig/cron/v3"
	"gorm.io/gorm"

	"taskbackend/internal/config"
	"taskbackend/internal/model"
	"taskbackend/internal/service"
)

type Scheduler struct {
	cron *cron.Cron
	db   *gorm.DB
	cfg  *config.Config
}

func New(db *gorm.DB, cfg *config.Config) *Scheduler {
	return &Scheduler{db: db, cfg: cfg}
}

func (s *Scheduler) Start() {
	s.cron = cron.New(cron.WithSeconds())
	// 每 30 秒扫描到期提醒
	if _, err := s.cron.AddFunc("*/30 * * * * *", s.checkReminders); err != nil {
		log.Printf("scheduler: %v", err)
	}
	// 每 10 分钟扫描逾期任务生成提醒
	if _, err := s.cron.AddFunc("0 */10 * * * *", s.checkOverdueTasks); err != nil {
		log.Printf("scheduler: %v", err)
	}
	s.cron.Start()
	log.Println("定时任务已启动")
}

func (s *Scheduler) Stop() {
	if s.cron != nil {
		ctx := s.cron.Stop()
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
	}
}

// checkReminders 到达提醒时间的提醒：标记已发送并推送外部通知
func (s *Scheduler) checkReminders() {
	now := time.Now()
	var due []model.Reminder
	s.db.Where("sent = ? AND remind_at <= ?", false, now).Find(&due)
	for i := range due {
		r := &due[i]
		r.Sent = true
		r.SentAt = &now
		title := "任务提醒"
		var task model.Task
		if err := s.db.Select("title").First(&task, r.TaskID).Error; err == nil {
			title = "任务提醒：" + task.Title
		}
		emailTo := ""
		if r.UserID != nil {
			var user model.User
			if err := s.db.Select("email").First(&user, *r.UserID).Error; err == nil {
				emailTo = user.Email
			}
		}
		msg := r.Message
		if msg == "" {
			msg = title
		}
		service.SendWebhook(s.cfg, title, msg)
		service.SendEmail(s.cfg, emailTo, title, msg)
		s.db.Save(r)
	}
}

// checkOverdueTasks 逾期未完成任务：每天为负责人生成一条逾期提醒
func (s *Scheduler) checkOverdueTasks() {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)

	var overdue []model.Task
	s.db.Where("status != ? AND due_date IS NOT NULL AND due_date < ?", model.StatusDone, now).Find(&overdue)

	for i := range overdue {
		t := &overdue[i]
		var last model.Reminder
		found := s.db.Where("task_id = ? AND remind_type = ?", t.ID, model.RemindOverdue).
			Order("created_at DESC").First(&last).Error == nil
		if found && last.CreatedAt.After(todayStart) {
			continue
		}
		msg := "任务「" + t.Title + "」已逾期，请尽快处理"
		reminder := model.Reminder{
			TaskID:     t.ID,
			UserID:     t.AssigneeID,
			RemindAt:   now,
			RemindType: model.RemindOverdue,
			Message:    msg,
		}
		s.db.Create(&reminder)
	}
}
