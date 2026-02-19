package database

import (
	"context"
	"errors"
	"fmt"
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
	PeerID         string    `gorm:"primaryKey;not null"`
	LastRemoteAddr string    // 最近一次看到的远端连接地址
	LastSeenAt     time.Time `gorm:"index"`
}

type sqliteTableColumn struct {
	Name string `gorm:"column:name"`
	PK   int    `gorm:"column:pk"`
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

	if err := migratePeerSchema(DB); err != nil {
		return err
	}

	// 初始化默认设置
	if err := initDefaultSettings(); err != nil {
		return err
	}

	return nil
}

func migratePeerSchema(db *gorm.DB) error {
	var columns []sqliteTableColumn
	if err := db.Raw("PRAGMA table_info(peers)").Scan(&columns).Error; err != nil {
		return err
	}

	if len(columns) == 0 {
		return nil
	}

	hasID := false
	hasAddrs := false
	hasLastRemoteAddr := false
	hasLastSeenAt := false
	peerIDIsPrimary := false
	for _, col := range columns {
		switch col.Name {
		case "id":
			hasID = true
		case "addrs":
			hasAddrs = true
		case "last_remote_addr":
			hasLastRemoteAddr = true
		case "last_seen_at":
			hasLastSeenAt = true
		case "peer_id":
			if col.PK == 1 {
				peerIDIsPrimary = true
			}
		}
	}

	needsRebuild := hasID || !peerIDIsPrimary
	if !needsRebuild {
		if hasAddrs && hasLastRemoteAddr {
			if err := db.Exec(`
				UPDATE peers
				SET last_remote_addr = addrs
				WHERE (last_remote_addr IS NULL OR last_remote_addr = '')
				  AND addrs IS NOT NULL
				  AND addrs <> ''
			`).Error; err != nil {
				return err
			}
			if err := db.Migrator().DropColumn(&Peer{}, "addrs"); err != nil {
				return err
			}
		}
		return nil
	}

	sourceAddrExpr := "''"
	switch {
	case hasLastRemoteAddr && hasAddrs:
		sourceAddrExpr = "CASE WHEN COALESCE(last_remote_addr, '') <> '' THEN last_remote_addr WHEN COALESCE(addrs, '') <> '' THEN addrs ELSE '' END"
	case hasLastRemoteAddr:
		sourceAddrExpr = "COALESCE(last_remote_addr, '')"
	case hasAddrs:
		sourceAddrExpr = "COALESCE(addrs, '')"
	}

	sourceLastSeenExpr := "CURRENT_TIMESTAMP" // SQLite CURRENT_TIMESTAMP is UTC.
	if hasLastSeenAt {
		sourceLastSeenExpr = "last_seen_at"
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`
			CREATE TABLE peers_new (
				peer_id TEXT PRIMARY KEY NOT NULL,
				last_remote_addr TEXT,
				last_seen_at DATETIME
			)
		`).Error; err != nil {
			return err
		}

		copySQL := fmt.Sprintf(`
			INSERT INTO peers_new (peer_id, last_remote_addr, last_seen_at)
			SELECT peer_id, %s, %s
			FROM peers
			WHERE COALESCE(peer_id, '') <> ''
			ON CONFLICT(peer_id) DO UPDATE SET
				last_remote_addr = excluded.last_remote_addr,
				last_seen_at = excluded.last_seen_at
		`, sourceAddrExpr, sourceLastSeenExpr)
		if err := tx.Exec(copySQL).Error; err != nil {
			return err
		}

		if err := tx.Exec(`DROP TABLE peers`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`ALTER TABLE peers_new RENAME TO peers`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_peers_last_seen_at ON peers(last_seen_at)`).Error; err != nil {
			return err
		}
		return nil
	})
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
		PeerID:         peerID,
		LastRemoteAddr: remoteAddr,
		LastSeenAt:     time.Now().UTC(),
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "peer_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"last_remote_addr": peer.LastRemoteAddr,
				"last_seen_at":     peer.LastSeenAt,
			}),
		}).Create(&peer).Error
	})
}
