package database

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"p2pos/internal/events"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
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
	LastPingRTTMs  *float64
	LastPingOK     bool
	LastPingAt     *time.Time `gorm:"index"`
	Reachability   string
	ObservedBy     string
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
	database, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gormlogger.New(
			log.New(os.Stdout, "", log.LstdFlags),
			gormlogger.Config{
				IgnoreRecordNotFoundError: true,
				LogLevel:                  gormlogger.Error,
			},
		),
	})
	if err != nil {
		return err
	}

	DB = database
	if err := configureSQLite(DB); err != nil {
		return err
	}

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

func configureSQLite(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}

	// SQLite single-writer model: one connection avoids cross-connection write lock contention.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	// Improve concurrent read/write behavior and wait for lock instead of failing fast.
	// Some filesystems (e.g. certain mounted network/host filesystems) may not support WAL.
	if err := db.Exec("PRAGMA journal_mode=WAL;").Error; err != nil {
		fmt.Printf("[DB] WAL not available, fallback to DELETE journal mode: %v\n", err)
		if fallbackErr := db.Exec("PRAGMA journal_mode=DELETE;").Error; fallbackErr != nil {
			fmt.Printf("[DB] Failed to switch journal mode to DELETE: %v\n", fallbackErr)
		}
	}
	if err := db.Exec("PRAGMA synchronous=NORMAL;").Error; err != nil {
		fmt.Printf("[DB] Failed to set synchronous=NORMAL: %v\n", err)
	}
	if err := db.Exec("PRAGMA busy_timeout=5000;").Error; err != nil {
		fmt.Printf("[DB] Failed to set busy_timeout: %v\n", err)
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
	hasLastPingRTTMs := false
	hasLastPingOK := false
	hasLastPingAt := false
	hasReachability := false
	hasObservedBy := false
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
		case "last_ping_rtt_ms":
			hasLastPingRTTMs = true
		case "last_ping_ok":
			hasLastPingOK = true
		case "last_ping_at":
			hasLastPingAt = true
		case "reachability":
			hasReachability = true
		case "observed_by":
			hasObservedBy = true
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
	sourcePingRTTExpr := "NULL"
	if hasLastPingRTTMs {
		sourcePingRTTExpr = "last_ping_rtt_ms"
	}
	sourcePingOKExpr := "0"
	if hasLastPingOK {
		sourcePingOKExpr = "last_ping_ok"
	}
	sourcePingAtExpr := "NULL"
	if hasLastPingAt {
		sourcePingAtExpr = "last_ping_at"
	}
	sourceReachabilityExpr := "'unknown'"
	if hasReachability {
		sourceReachabilityExpr = "COALESCE(reachability, 'unknown')"
	}
	sourceObservedByExpr := "''"
	if hasObservedBy {
		sourceObservedByExpr = "COALESCE(observed_by, '')"
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`
			CREATE TABLE peers_new (
				peer_id TEXT PRIMARY KEY NOT NULL,
				last_remote_addr TEXT,
				last_seen_at DATETIME,
				last_ping_rtt_ms REAL,
				last_ping_ok NUMERIC,
				last_ping_at DATETIME,
				reachability TEXT,
				observed_by TEXT
			)
		`).Error; err != nil {
			return err
		}

		copySQL := fmt.Sprintf(`
			INSERT INTO peers_new (peer_id, last_remote_addr, last_seen_at, last_ping_rtt_ms, last_ping_ok, last_ping_at, reachability, observed_by)
			SELECT peer_id, %s, %s, %s, %s, %s, %s, %s
			FROM peers
			WHERE COALESCE(peer_id, '') <> ''
			ON CONFLICT(peer_id) DO UPDATE SET
				last_remote_addr = excluded.last_remote_addr,
				last_seen_at = excluded.last_seen_at,
				last_ping_rtt_ms = excluded.last_ping_rtt_ms,
				last_ping_ok = excluded.last_ping_ok,
				last_ping_at = excluded.last_ping_at,
				reachability = excluded.reachability,
				observed_by = excluded.observed_by
		`, sourceAddrExpr, sourceLastSeenExpr, sourcePingRTTExpr, sourcePingOKExpr, sourcePingAtExpr, sourceReachabilityExpr, sourceObservedByExpr)
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

func (r *PeerRepository) UpsertLastSeen(_ context.Context, peerID, remoteAddr, observedBy, reachability string) error {
	now := time.Now().UTC()
	peer := Peer{
		PeerID:         peerID,
		LastRemoteAddr: remoteAddr,
		LastSeenAt:     now,
		Reachability:   reachability,
		ObservedBy:     observedBy,
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "peer_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"last_remote_addr": peer.LastRemoteAddr,
				"last_seen_at":     peer.LastSeenAt,
				"reachability":     peer.Reachability,
				"observed_by":      peer.ObservedBy,
			}),
		}).Create(&peer).Error
	})
}

func (r *PeerRepository) UpdatePingResult(_ context.Context, peerID, observedBy string, ok bool, rtt time.Duration) error {
	now := time.Now().UTC()

	var rttMs *float64
	if ok {
		v := float64(rtt.Microseconds()) / 1000.0
		rttMs = &v
	}

	reachability := "disconnected"
	if ok {
		reachability = "connected"
	}

	peer := Peer{
		PeerID:        peerID,
		LastSeenAt:    now,
		LastPingAt:    &now,
		LastPingOK:    ok,
		LastPingRTTMs: rttMs,
		Reachability:  reachability,
		ObservedBy:    observedBy,
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "peer_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"last_ping_ok":     peer.LastPingOK,
				"last_ping_at":     peer.LastPingAt,
				"last_ping_rtt_ms": peer.LastPingRTTMs,
				"observed_by":      peer.ObservedBy,
				"reachability":     peer.Reachability,
				"last_seen_at":     peer.LastSeenAt,
			}),
		}).Create(&peer).Error
	})
}

func (r *PeerRepository) UpdateReachability(_ context.Context, peerID, observedBy, reachability string) error {
	now := time.Now().UTC()
	peer := Peer{
		PeerID:       peerID,
		LastSeenAt:   now,
		Reachability: reachability,
		ObservedBy:   observedBy,
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "peer_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"reachability": peer.Reachability,
				"observed_by":  peer.ObservedBy,
				"last_seen_at": peer.LastSeenAt,
			}),
		}).Create(&peer).Error
	})
}

func (r *PeerRepository) ListPeerStatuses(_ context.Context) ([]Peer, error) {
	var peers []Peer
	if err := DB.Order("peer_id asc").Find(&peers).Error; err != nil {
		return nil, err
	}
	return peers, nil
}

func (r *PeerRepository) UpsertSelf(_ context.Context, peerID string) error {
	now := time.Now().UTC()
	peer := Peer{
		PeerID:       peerID,
		LastSeenAt:   now,
		Reachability: "self",
		ObservedBy:   peerID,
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "peer_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"last_seen_at":     peer.LastSeenAt,
				"reachability":     peer.Reachability,
				"observed_by":      peer.ObservedBy,
				"last_ping_ok":     false,
				"last_ping_at":     nil,
				"last_ping_rtt_ms": nil,
			}),
		}).Create(&peer).Error
	})
}

func (r *PeerRepository) UpsertDiscovered(_ context.Context, peerID, addr, observedBy string) error {
	now := time.Now().UTC()
	peer := Peer{
		PeerID:         peerID,
		LastRemoteAddr: addr,
		LastSeenAt:     now,
		Reachability:   "discovered",
		ObservedBy:     observedBy,
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "peer_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"last_remote_addr": peer.LastRemoteAddr,
				"last_seen_at":     peer.LastSeenAt,
				"observed_by":      peer.ObservedBy,
				"reachability": gorm.Expr(
					"CASE WHEN reachability IN ('connected','self') THEN reachability ELSE ? END",
					peer.Reachability,
				),
			}),
		}).Create(&peer).Error
	})
}

func (r *PeerRepository) MergeObservedState(_ context.Context, state events.PeerStateObserved) error {
	if state.PeerID == "" {
		return nil
	}

	incoming := Peer{
		PeerID:         state.PeerID,
		LastRemoteAddr: state.RemoteAddr,
		LastSeenAt:     state.LastSeenAt.UTC(),
		LastPingRTTMs:  state.LastPingRTTMs,
		LastPingOK:     state.LastPingOK,
		LastPingAt:     cloneUTC(state.LastPingAt),
		Reachability:   state.Reachability,
		ObservedBy:     state.ObservedBy,
	}
	if incoming.LastSeenAt.IsZero() {
		incoming.LastSeenAt = time.Now().UTC()
	}
	if incoming.Reachability == "" {
		incoming.Reachability = "discovered"
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		var existing Peer
		err := tx.Where("peer_id = ?", incoming.PeerID).First(&existing).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return tx.Create(&incoming).Error
			}
			return err
		}

		if existing.Reachability == "self" {
			return nil
		}

		existingUpdated := peerUpdatedAt(existing)
		incomingUpdated := state.ObservedAt.UTC()
		if incomingUpdated.IsZero() {
			incomingUpdated = peerUpdatedAt(incoming)
		}

		updates := map[string]interface{}{}
		if existing.LastRemoteAddr == "" && incoming.LastRemoteAddr != "" {
			updates["last_remote_addr"] = incoming.LastRemoteAddr
		}

		if incomingUpdated.After(existingUpdated) {
			updates["last_seen_at"] = incoming.LastSeenAt
			updates["last_ping_ok"] = incoming.LastPingOK
			updates["last_ping_at"] = incoming.LastPingAt
			updates["last_ping_rtt_ms"] = incoming.LastPingRTTMs
			updates["observed_by"] = incoming.ObservedBy
			if existing.Reachability != "connected" && existing.Reachability != "self" {
				updates["reachability"] = incoming.Reachability
			}
			if incoming.LastRemoteAddr != "" {
				updates["last_remote_addr"] = incoming.LastRemoteAddr
			}
		}

		if len(updates) == 0 {
			return nil
		}
		return tx.Model(&Peer{}).Where("peer_id = ?", incoming.PeerID).Updates(updates).Error
	})
}

func cloneUTC(v *time.Time) *time.Time {
	if v == nil {
		return nil
	}
	t := v.UTC()
	return &t
}

func peerUpdatedAt(p Peer) time.Time {
	if p.LastPingAt != nil && !p.LastPingAt.IsZero() {
		return p.LastPingAt.UTC()
	}
	return p.LastSeenAt.UTC()
}

func (r *PeerRepository) DeleteDisconnectedBefore(_ context.Context, cutoff time.Time) error {
	return DB.Where("reachability = ? AND last_seen_at < ?", "disconnected", cutoff.UTC()).Delete(&Peer{}).Error
}
