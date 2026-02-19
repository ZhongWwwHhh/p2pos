package database

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var DB *gorm.DB

const (
	settingKeyVersion  = "version"
	settingKeyNodePriv = "nodePriv"
)

// Setting 键值对存储
type Setting struct {
	Key   string `gorm:"primaryKey"`
	Value string
}

// Peer 对等节点信息
type Peer struct {
	ID         uint      `gorm:"primaryKey"`
	PeerID     string    `gorm:"uniqueIndex;not null"`
	Addrs      string    // 当前连接地址
	LastSeenAt time.Time `gorm:"index"`
}

// Init 初始化数据库连接
func Init() error {
	// 获取执行文件所在目录
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	exeDir := filepath.Dir(exePath)
	dbPath := filepath.Join(exeDir, "sqlite.db")

	// 打开或创建数据库
	database, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		return err
	}

	DB = database

	// 自动迁移表结构
	if err := DB.AutoMigrate(&Setting{}, &Peer{}); err != nil {
		return err
	}

	// 初始化默认设置
	if err := initDefaultSettings(); err != nil {
		return err
	}

	return nil
}

// initDefaultSettings 初始化默认设置值
func initDefaultSettings() error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := createSettingIfMissing(tx, settingKeyVersion, "00000000-0000"); err != nil {
			return err
		}
		if err := createSettingIfMissing(tx, settingKeyNodePriv, ""); err != nil {
			return err
		}
		return nil
	})
}

func createSettingIfMissing(tx *gorm.DB, key, defaultValue string) error {
	return tx.Clauses(clause.OnConflict{DoNothing: true}).
		Create(&Setting{Key: key, Value: defaultValue}).Error
}

func upsertSetting(tx *gorm.DB, key, value string) error {
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&Setting{Key: key, Value: value}).Error
}

func getSettingOrDefault(key, fallback string) (string, error) {
	var s Setting
	err := DB.Where("key = ?", key).First(&s).Error
	if err == nil {
		return s.Value, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fallback, nil
	}
	return "", err
}

// LoadNodePrivateKey 读取节点私钥
func LoadNodePrivateKey() (string, error) {
	return getSettingOrDefault(settingKeyNodePriv, "")
}

// SaveNodePrivateKey 保存节点私钥
func SaveNodePrivateKey(nodePriv string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		return upsertSetting(tx, settingKeyNodePriv, nodePriv)
	})
}

// LoadNodeSetting 读取节点设置
func LoadNodeSetting() (*NodeSetting, error) {
	version, err := getSettingOrDefault(settingKeyVersion, "00000000-0000")
	if err != nil {
		return nil, err
	}

	nodePriv, err := getSettingOrDefault(settingKeyNodePriv, "")
	if err != nil {
		return nil, err
	}

	return &NodeSetting{
		Version:  version,
		NodePriv: nodePriv,
	}, nil
}

// SaveNodeSetting 保存或更新节点设置
func SaveNodeSetting(ns *NodeSetting) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := upsertSetting(tx, settingKeyVersion, ns.Version); err != nil {
			return err
		}
		if err := upsertSetting(tx, settingKeyNodePriv, ns.NodePriv); err != nil {
			return err
		}
		return nil
	})
}

// NodeSetting 节点设置信息
type NodeSetting struct {
	Version  string
	NodePriv string
}

type PeerRepository struct{}

func NewPeerRepository() *PeerRepository {
	return &PeerRepository{}
}

func (r *PeerRepository) UpsertLastSeen(_ context.Context, peerID, remoteAddr string) error {
	peer := Peer{
		PeerID:     peerID,
		Addrs:      remoteAddr,
		LastSeenAt: time.Now(),
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "peer_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"addrs":        peer.Addrs,
				"last_seen_at": peer.LastSeenAt,
			}),
		}).Create(&peer).Error
	})
}
