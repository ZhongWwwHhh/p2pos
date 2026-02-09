package sqlite

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"p2pos/internal/config"
	"sync"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	_ "github.com/mattn/go-sqlite3"
)

var (
	dbInstance *sql.DB
	once       sync.Once
)

// Init 初始化数据库连接
func Init() error {
	var err error
	once.Do(func() {
		dbInstance, err = sql.Open("sqlite3", config.SqliteDatabasePath)
	})
	return err
}

// GetDB 获取全局数据库连接
func GetDB() *sql.DB {
	return dbInstance
}

// Close 关闭数据库连接
func Close() error {
	if dbInstance != nil {
		return dbInstance.Close()
	}
	return nil
}

func GetInitConnections() ([]config.InitConnection, error) {
	db := GetDB()
	var initConnStr string
	err := db.QueryRow(`SELECT value FROM config WHERE key = ?;`, "init_connections").Scan(&initConnStr)
	if err != nil {
		return nil, err
	}
	var initConns []config.InitConnection
	if err := json.Unmarshal([]byte(initConnStr), &initConns); err != nil {
		return nil, err
	}
	return initConns, nil
}

func GetPeerIDAndKeys() (peer.ID, crypto.PrivKey, crypto.PubKey, error) {
	db := GetDB()
	var peerIDStr, privKeyStr, pubKeyStr string
	err := db.QueryRow(`SELECT value FROM config WHERE key = ?;`, "peer_id").Scan(&peerIDStr)
	if err != nil {
		return "", nil, nil, err
	}
	err = db.QueryRow(`SELECT value FROM config WHERE key = ?;`, "peer_private_key").Scan(&privKeyStr)
	if err != nil {
		return "", nil, nil, err
	}
	err = db.QueryRow(`SELECT value FROM config WHERE key = ?;`, "peer_public_key").Scan(&pubKeyStr)
	if err != nil {
		return "", nil, nil, err
	}
	privBytes, err := base64.StdEncoding.DecodeString(privKeyStr)
	if err != nil {
		return "", nil, nil, err
	}
	privKey, err := crypto.UnmarshalPrivateKey(privBytes)
	if err != nil {
		return "", nil, nil, err
	}
	pubBytes, err := base64.StdEncoding.DecodeString(pubKeyStr)
	if err != nil {
		return "", nil, nil, err
	}
	pubKey, err := crypto.UnmarshalPublicKey(pubBytes)
	if err != nil {
		return "", nil, nil, err
	}
	peerID, err := peer.IDFromPublicKey(pubKey)
	if err != nil {
		return "", nil, nil, err
	}
	return peerID, privKey, pubKey, nil
}

func WriteInitTable(initCfg *config.InitConfig) error {
	if err := Init(); err != nil {
		return err
	}
	db := GetDB()

	// 创建 peers 表
	_, err := db.Exec(`
        CREATE TABLE IF NOT EXISTS peers (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            peer_id TEXT UNIQUE,         -- libp2p Peer ID
            name TEXT,                   -- 可读名称
            address TEXT,                -- Multiaddr 格式
            public_key TEXT,             -- 可选公钥
            last_connected DATETIME,     -- 可选最后连接时间
            remark TEXT,                 -- 可选备注
            system_key TEXT,             -- 所属系统公钥
            FOREIGN KEY(system_key) REFERENCES systems(public_key)
        );
    `)
	if err != nil {
		return err
	}

	// peers 表 last_connected 字段索引
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_peers_last_connected ON peers(last_connected);`)
	if err != nil {
		return err
	}
	// peers 表 system_key 索引
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_peers_system_key ON peers(system_key);`)
	if err != nil {
		return err
	}

	// 创建 systems 表
	_, err = db.Exec(`
        CREATE TABLE IF NOT EXISTS systems (
            public_key TEXT PRIMARY KEY,   -- 系统公钥，唯一标识
            name TEXT                      -- 可读名称
        );
    `)
	if err != nil {
		return err
	}

	// 创建 config 表
	_, err = db.Exec(`
        CREATE TABLE IF NOT EXISTS config (
            key TEXT PRIMARY KEY,    -- 配置项名称
            value TEXT               -- 配置项内容
        );
    `)
	if err != nil {
		return err
	}

	// 创建 records 表
	_, err = db.Exec(`
        CREATE TABLE IF NOT EXISTS records (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            data TEXT,
            timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
        );
    `)
	if err != nil {
		return err
	}

	// records 表 timestamp 索引
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_records_timestamp ON records(timestamp);`)
	if err != nil {
		return err
	}

	// 插入初始数据
	_, err = db.Exec(`INSERT OR IGNORE INTO systems (public_key, name) VALUES (?, ?);`, initCfg.SystemPubKey, initCfg.SystemPubKey)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT OR IGNORE INTO config (key, value) VALUES (?, ?);`, "system_pub_key", initCfg.SystemPubKey)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT OR IGNORE INTO config (key, value) VALUES (?, ?);`, "listen_address", initCfg.ListenAddr)
	if err != nil {
		return err
	}
	initConnStr, err := json.Marshal(initCfg.InitConnections)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT OR IGNORE INTO config (key, value) VALUES (?, ?);`, "init_connections", string(initConnStr))
	if err != nil {
		return err
	}

	// 生成节点密钥对
	priv, pub, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		return err
	}
	// 编码私钥和公钥为 base64
	privBytes, err := crypto.MarshalPrivateKey(priv)
	if err != nil {
		return err
	}
	pubBytes, err := crypto.MarshalPublicKey(pub)
	if err != nil {
		return err
	}
	privStr := base64.StdEncoding.EncodeToString(privBytes)
	pubStr := base64.StdEncoding.EncodeToString(pubBytes)

	_, err = db.Exec(`INSERT OR IGNORE INTO config (key, value) VALUES (?, ?);`, "peer_private_key", privStr)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT OR IGNORE INTO config (key, value) VALUES (?, ?);`, "peer_public_key", pubStr)
	if err != nil {
		return err
	}

	return nil
}
