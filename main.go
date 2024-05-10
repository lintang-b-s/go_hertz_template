// Code generated by hertz generator.

package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"regexp"
	"time"

	"github.com/cloudwego/hertz/pkg/common/hlog"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/hertz-contrib/pprof"

	"lintang/go_hertz_template/biz/dal"
	"lintang/go_hertz_template/biz/router"
	"lintang/go_hertz_template/biz/util/jwt"
	"lintang/go_hertz_template/config"
	"lintang/go_hertz_template/di"
	hello "lintang/go_hertz_template/kitex_gen/go_hertz_template_lintang/pb/helloservice"

	"github.com/cloudwego/kitex/pkg/transmeta"
	kitexServer "github.com/cloudwego/kitex/server"
	hertzzap "github.com/hertz-contrib/logger/zap"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/app/server/binding"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/natefinch/lumberjack"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	cfg, err := config.NewConfig()
	if err != nil {
		hlog.Fatalf("Config error: %s", err)
	}
	logsCores := initZapLogger(cfg)
	defer logsCores.Sync()
	hlog.SetLogger(logsCores)

	// init data access layer
	pg := dal.InitPg(cfg) // init postgres & rabbitmq

	// validation error custom
	customValidationErr := CreateCustomValidationError()
	h := server.Default(
		server.WithHostPorts(fmt.Sprintf(`0.0.0.0:%s`, cfg.HTTP.Port)),
		server.WithValidateConfig(customValidationErr),
		server.WithExitWaitTime(4*time.Second),
		server.WithValidateConfig(passwordValidator()),
	)
	h.Use(AccessLog())

	pprof.Register(h)
	var callback []route.CtxCallback

	callback = append(callback, pg.ClosePostgres)
	h.Engine.OnShutdown = append(h.Engine.OnShutdown, callback...) /// graceful shutdown
	uSvc := di.InitUserService(pg, cfg)
	aSvc := di.InitAuthService(pg, cfg)
	jwt := jwt.NewJWTMaker(cfg)
	router.UserRouter(h, uSvc, jwt)
	router.AuthRouter(h, aSvc)

	addr, _ := net.ResolveTCPAddr("tcp", fmt.Sprintf(`127.0.0.1:%s`, cfg.GRPC.URLGrpc)) // grpc address
	var opts []kitexServer.Option
	opts = append(opts, kitexServer.WithMetaHandler(transmeta.ServerHTTP2Handler))
	opts = append(opts, kitexServer.WithServiceAddr(addr))
	srv := hello.NewServer(new(HelloServiceImpl), opts...) //grpc server

	go func() {
		// start kitex rpc server (grpc)
		err := srv.Run()
		if err != nil {
			log.Fatal(err)
		}
	}()

	// start hertz http server
	h.Spin()

}

func passwordValidator() *binding.ValidateConfig {
	// golang gabisa pake regex '^?=.*[0-9]?=.*[a-z]?=.*[A-Z]?=.*\\W(?!.* )*$'
	passwordVDConfig := &binding.ValidateConfig{}
	passwordVDConfig.MustRegValidateFunc("password", func(args ...interface{}) error {
		password, _ := args[0].(string)
		lengthRegex := regexp.MustCompile(`.{8,}`)        // Memeriksa panjang minimal 8 karakter
		upperRegex := regexp.MustCompile(`[A-Z]`)         // Memeriksa adanya huruf besar
		lowerRegex := regexp.MustCompile(`[a-z]`)         // Memeriksa adanya huruf kecil
		numberRegex := regexp.MustCompile(`\d`)           // Memeriksa adanya angka
		specialCharRegex := regexp.MustCompile(`[@#$%*]`) // Memeriksa adanya karakter spesial

		isMatch := lengthRegex.MatchString(password) &&
			upperRegex.MatchString(password) &&
			lowerRegex.MatchString(password) &&
			numberRegex.MatchString(password) &&
			specialCharRegex.MatchString(password)
		if !isMatch {
			return fmt.Errorf("password harus terdiri dari minimal 8 karakter, 1 uppercase, 1 lowercase, 1 digit, 1 karakter spesial")
		}
		return nil
	})
	return passwordVDConfig
}

var lg *zap.Logger

// pake hertzlogger gak kayak pake uber/zap logger beneran
func initZapLogger(cfg *config.Config) *hertzzap.Logger {
	productionCfg := zap.NewProductionEncoderConfig()
	productionCfg.TimeKey = "timestamp"
	productionCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	productionCfg.EncodeDuration = zapcore.SecondsDurationEncoder
	productionCfg.EncodeCaller = zapcore.ShortCallerEncoder

	developmentCfg := zap.NewDevelopmentEncoderConfig()
	developmentCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder

	// log encooder (json for prod, console for dev)
	consoleEncoder := zapcore.NewConsoleEncoder(developmentCfg)
	fileEncoder := zapcore.NewJSONEncoder(productionCfg)
	// loglevel
	logDevLevel := zap.NewAtomicLevelAt(zap.DebugLevel)
	logLevelProd := zap.NewAtomicLevelAt(zap.InfoLevel)

	//write sycer
	writeSyncerStdout, writeSyncerFile := getLogWriter(cfg.MaxBackups, cfg.MaxAge)

	prodCfg := hertzzap.CoreConfig{
		Enc: fileEncoder,
		Ws:  writeSyncerFile,
		Lvl: logLevelProd,
	}

	devCfg := hertzzap.CoreConfig{
		Enc: consoleEncoder,
		Ws:  writeSyncerStdout,
		Lvl: logDevLevel,
	}
	logsCores := []hertzzap.CoreConfig{
		prodCfg,
		devCfg,
	}
	coreConsole := zapcore.NewCore(consoleEncoder, writeSyncerStdout, logDevLevel)
	coreFile := zapcore.NewCore(fileEncoder, writeSyncerFile, logLevelProd)
	core := zapcore.NewTee(
		coreConsole,
		coreFile,
	)
	lg = zap.New(core)
	zap.ReplaceGlobals(lg)

	prodAndDevLogger := hertzzap.NewLogger(hertzzap.WithZapOptions(zap.WithFatalHook(zapcore.WriteThenPanic)),
		hertzzap.WithCores(logsCores...))

	return prodAndDevLogger
}

func getLogWriter(maxBackup, maxAge int) (writeSyncerStdout zapcore.WriteSyncer, writeSyncerFile zapcore.WriteSyncer) {
	file := zapcore.AddSync(&lumberjack.Logger{
		Filename: "./logs/app.log",

		MaxBackups: maxBackup,
		MaxAge:     maxAge,
	})
	stdout := zapcore.AddSync(os.Stdout)

	return stdout, file
}

type ValidateError struct {
	ErrType, FailField, Msg string
}

// Error implements error interface.
func (e *ValidateError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return e.ErrType + ": expr_path=" + e.FailField + ", cause=invalid"
}

type BindError struct {
	ErrType, FailField, Msg string
}

// Error implements error interface.
func (e *BindError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return e.ErrType + ": expr_path=" + e.FailField + ", cause=invalid"
}

func CreateCustomValidationError() *binding.ValidateConfig {
	validateConfig := &binding.ValidateConfig{}
	validateConfig.SetValidatorErrorFactory(func(failField, msg string) error {
		err := ValidateError{
			ErrType:   "validateErr",
			FailField: "[validateFailField]: " + failField,
			Msg:       msg,
		}

		return &err
	})
	return validateConfig
}

// accessLogger nbawaan zap bagus ini pas di load testing
func AccessLog() app.HandlerFunc {
	return func(c context.Context, ctx *app.RequestContext) {
		start := time.Now()
		path := string(ctx.Request.URI().Path()[:])
		query := string(ctx.Request.URI().QueryString()[:])
		ctx.Next(c)
		cost := time.Since(start)
		lg.Info(path,
			zap.Int("status", ctx.Response.StatusCode()),
			zap.String("method", string(ctx.Request.Header.Method())),
			zap.String("path", path),
			zap.String("query", query),
			zap.String("ip", ctx.ClientIP()),
			zap.String("user-agent", string(ctx.Host())),
			zap.String("errors", ctx.Errors.String()),
			zap.Duration("cost", cost),
		)
	}
}
