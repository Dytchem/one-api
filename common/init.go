package common

import (
	"flag"
	"fmt"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/common/random"
	"log"
	"os"
	"path/filepath"
	"strings"
)

var (
	Port         = flag.Int("port", 3000, "the listening port")
	PrintVersion = flag.Bool("version", false, "print version and exit")
	PrintHelp    = flag.Bool("help", false, "print help and exit")
	LogDir       = flag.String("log-dir", "./logs", "specify the log directory")
)

func printHelp() {
	fmt.Println("One API " + Version + " - All in one API service for OpenAI API.")
	fmt.Println("Copyright (C) 2023 JustSong. All rights reserved.")
	fmt.Println("GitHub: https://github.com/songquanpeng/one-api")
	fmt.Println("Usage: one-api [--port <port>] [--log-dir <log directory>] [--version] [--help]")
}

func Init() {
	flag.Parse()

	if *PrintVersion {
		fmt.Println(Version)
		os.Exit(0)
	}

	if *PrintHelp {
		printHelp()
		os.Exit(0)
	}

	if os.Getenv("SQLITE_PATH") != "" {
		SQLitePath = os.Getenv("SQLITE_PATH")
	}

	if os.Getenv("SESSION_SECRET") != "" {
		if os.Getenv("SESSION_SECRET") == "random_string" {
			logger.FatalLog("SESSION_SECRET is set to the example value \"random_string\", refusing to start. Please set a random secret (e.g. openssl rand -hex 32).")
		} else {
			config.SessionSecret = os.Getenv("SESSION_SECRET")
		}
	}
	// dyt-93: 未显式设置 SESSION_SECRET 时，从数据目录读取/生成并持久化，
	// 保证首次启动自动生成随机密钥、重启不变（compose 不支持命令替换，不能依赖 ${VAR:-$(cmd)}）。
	// 注意：SQLITE_PATH 解析须在 SESSION_SECRET 处理之前（上面已处理）。
	if config.SessionSecret == "" || os.Getenv("SESSION_SECRET") == "" {
		secretFile := filepath.Join(filepath.Dir(SQLitePath), "session_secret")
		if data, err := os.ReadFile(secretFile); err == nil {
			secret := strings.TrimSpace(string(data))
			if secret == "" {
				logger.FatalLog("session secret file " + secretFile + " is empty, refusing to start. Remove it to generate a new one.")
			}
			config.SessionSecret = secret
		} else {
			config.SessionSecret = random.GetRandomString(32)
			// dyt-93: 持久化失败必须拒绝启动——否则每次重启密钥变化，所有会话静默失效
			if err := os.MkdirAll(filepath.Dir(secretFile), 0755); err != nil {
				logger.FatalLog("cannot create data directory for session secret (" + secretFile + "): " + err.Error())
			}
			if err := os.WriteFile(secretFile, []byte(config.SessionSecret), 0600); err != nil {
				logger.FatalLog("cannot persist session secret to " + secretFile + ": " + err.Error())
			}
		}
	}
	if *LogDir != "" {
		var err error
		*LogDir, err = filepath.Abs(*LogDir)
		if err != nil {
			log.Fatal(err)
		}
		if _, err := os.Stat(*LogDir); os.IsNotExist(err) {
			err = os.Mkdir(*LogDir, 0777)
			if err != nil {
				log.Fatal(err)
			}
		}
		logger.LogDir = *LogDir
	}
}
