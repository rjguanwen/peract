package main

import (
	"log"

	"github.com/gin-gonic/gin"

	"taskbackend/internal/config"
	"taskbackend/internal/database"
	"taskbackend/internal/handler"
	"taskbackend/internal/middleware"
	"taskbackend/internal/scheduler"
)

func main() {
	cfg := config.Load()

	db, err := database.Open(cfg)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	database.Migrate(db)
	database.InitAdmin(db, cfg)

	sched := scheduler.New(db, cfg)
	sched.Start()
	defer sched.Stop()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())
	r.Use(middleware.CORS(cfg))

	auth := middleware.NewAuth(cfg, db)
	h := handler.New(db, cfg, auth)
	h.RegisterRoutes(r)

	// 上传文件静态访问（头像等，公开）
	r.Static("/uploads", cfg.UploadDir)

	addr := ":" + cfg.Port
	log.Printf("%s 已启动，监听 %s", cfg.ProjectName, addr)
	if err := r.Run(addr); err != nil {
		log.Fatalf("run server: %v", err)
	}
}
