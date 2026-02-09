package initializer

import (
	"os"
	"p2pos/internal/config"
	"p2pos/internal/logger"
	"p2pos/internal/sqlite"
)

// 检查是否需要初始化
func checkNeedInit() (bool, error) {
	// 检查 init_config.json 是否存在
	_, err := os.Stat(config.InitConfigPath)
	if err == nil {
		return true, nil
	}
	// 检查 sqlite 数据库文件是否存在
	_, err = os.Stat(config.SqliteDatabasePath)
	if err == nil {
		return false, nil
	}
	// 都不存在，错误
	return false, err
}

// 移除已用init_config.json
func RenameInitConfig() error {
	err := os.Rename(config.InitConfigPath, config.InitConfigPath+".used")
	if err != nil {
		logger.Error("Rename init config error", err)
		return err
	}
	return nil
}

func Init() error {
	needInit, err := checkNeedInit()
	if err != nil {
		logger.Error("Init error, config file or database file may be corrupted", err)
		return err
	}
	if needInit {
		logger.Info("Init config...", nil)
		initCfg, err := config.LoadInit()
		if err != nil {
			logger.Error("Load init config error", err)
			return err
		}
		logger.Info("Init database...", nil)
		if err := sqlite.WriteInitTable(initCfg); err != nil {
			logger.Error("Init database error", err)
			return err
		}
		if err := RenameInitConfig(); err != nil {
			logger.Error("Rename init config error", err)
			return err
		}
	}

	// 初始化数据库连接
	if err := sqlite.Init(); err != nil {
		logger.Error("Init sqlite error", err)
		return err
	}

	logger.Info("Init success", nil)
	return nil
}
