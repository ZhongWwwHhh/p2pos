package network

import (
	"encoding/base64"
	"fmt"

	"p2pos/internal/database"

	"github.com/libp2p/go-libp2p/core/crypto"
)

func LoadOrCreatePrivateKey() (crypto.PrivKey, error) {
	storedPrivKey, err := database.LoadNodePrivateKey()
	if err != nil {
		return nil, err
	}

	generateAndPersistNodeKey := func() (crypto.PrivKey, error) {
		generatedKey, _, err := crypto.GenerateEd25519Key(nil)
		if err != nil {
			return nil, err
		}

		privKeyBytes, err := crypto.MarshalPrivateKey(generatedKey)
		if err != nil {
			return nil, err
		}

		encodedPrivKey := base64.StdEncoding.EncodeToString(privKeyBytes)
		if err := database.SaveNodePrivateKey(encodedPrivKey); err != nil {
			return nil, err
		}

		fmt.Println("[NODE] Generated and persisted new node private key")
		return generatedKey, nil
	}

	if storedPrivKey == "" {
		return generateAndPersistNodeKey()
	}

	privKeyBytes, err := base64.StdEncoding.DecodeString(storedPrivKey)
	if err != nil {
		fmt.Printf("[NODE] Stored private key is invalid base64, regenerating: %v\n", err)
		return generateAndPersistNodeKey()
	}

	loadedKey, err := crypto.UnmarshalPrivateKey(privKeyBytes)
	if err != nil {
		fmt.Printf("[NODE] Stored private key is invalid, regenerating: %v\n", err)
		return generateAndPersistNodeKey()
	}

	fmt.Println("[NODE] Loaded persisted node private key")
	return loadedKey, nil
}
