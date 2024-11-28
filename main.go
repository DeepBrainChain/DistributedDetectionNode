package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"DistributedDetectionNode/db"
	hmp "DistributedDetectionNode/http"
	"DistributedDetectionNode/log"
	"DistributedDetectionNode/types"
	"DistributedDetectionNode/ws"

	"github.com/gin-gonic/gin"
)

var version string

var defaultLogFormatter = func(params gin.LogFormatterParams) string {
	var statusColor, methodColor, resetColor string
	if params.IsOutputColor() {
		statusColor = params.StatusCodeColor()
		methodColor = params.MethodColor()
		resetColor = params.ResetColor()
	}

	if params.Latency > time.Minute {
		params.Latency = params.Latency.Truncate(time.Second)
	}
	return fmt.Sprintf("[GIN] %v |%s %3d %s| %13v | %15s |%s %-7s %s %#v | %#v\n%s",
		params.TimeStamp.Format("2006/01/02 - 15:04:05"),
		statusColor, params.StatusCode, resetColor,
		params.Latency,
		params.ClientIP,
		methodColor, params.Method, resetColor,
		params.Path,
		params.Request.UserAgent(),
		params.ErrorMessage,
	)
}

func main() {
	configPath := flag.String("config", "", "run using the configuration file")
	versionFlag := flag.Bool("version", false, "show version number and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Println(version)
		os.Exit(0)
	}
	if *configPath == "" {
		fmt.Println("run command like 'app -config ./config.json'")
		os.Exit(1)
	}
	cfg, err := types.LoadConfig(*configPath)
	if err != nil {
		fmt.Println("Failed to load JSON configuration file:", err)
		os.Exit(1)
	}
	if err := log.InitLogrus(cfg.LogLevel, cfg.LogFile); err != nil {
		fmt.Println("Initialize the log failed:", err)
		os.Exit(1)
	}

	// Create context that listens for the interrupt signal from the OS.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := db.InitMongo(ctx, cfg.MongoDB.URI, cfg.MongoDB.Database, cfg.MongoDB.ExpireTime); err != nil {
		os.Exit(1)
	}

	pm := hmp.NewPrometheusMetrics(cfg.Prometheus.JobName)

	gin.DefaultWriter = log.Log.Out
	// router := gin.Default()
	router := gin.New()
	// router.Use(func(ctx *gin.Context) {
	// 	start := time.Now()
	// 	ctx.Next()
	// 	latency := time.Since(start)
	// 	log.Log.WithFields(logrus.Fields{
	// 		"client_ip":  ctx.ClientIP(),
	// 		"timestamp":  start.Format(time.RFC1123),
	// 		"method":     ctx.Request.Method,
	// 		"path":       ctx.Request.URL.Path,
	// 		"protocol":   ctx.Request.Proto,
	// 		"status":     ctx.Writer.Status(),
	// 		"latency":    latency,
	// 		"user_agent": ctx.Request.UserAgent(),
	// 		"errors":     ctx.Errors.ByType(gin.ErrorTypePrivate).String(),
	// 	}).Info("request details")
	// })
	router.Use(gin.LoggerWithFormatter(defaultLogFormatter), gin.Recovery())
	router.GET("/metrics/prometheus", pm.Metrics)
	// router.GET("/echo", ws.Echo)
	router.GET("/websocket", func(c *gin.Context) {
		ws.Ws(c, pm)
	})

	// log.Log.Fatal(router.Run(cfg.Addr))

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: router,
	}

	// Initializing the server in a goroutine so that
	// it won't block the graceful shutdown handling below
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Log.Fatalf("Start server: %v", err)
		}
	}()

	// Listen for the interrupt signal.
	<-ctx.Done()

	// Restore default behavior on the interrupt signal and notify user of shutdown.
	stop()
	log.Log.Println("shutting down gracefully, press Ctrl+C again to force")

	// The context is used to inform the server it has 5 seconds to finish
	// the request it is currently handling
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Log.Fatal("Server forced to shutdown: ", err)
	}

	log.Log.Println("Server exiting")
}
