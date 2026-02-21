package app

import (
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func RunKeygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	newSystem := fs.Bool("new-system", false, "generate system keypair and admin proof")
	clusterID := fs.String("cluster-id", "default", "cluster id")
	adminValidTo := fs.String("admin-valid-to", "9999-12-31T00:00:00Z", "admin proof valid_to (RFC3339/RFC3339Nano)")
	nodePriv := fs.String("node-priv", "", "existing node private key (base64), optional")

	if err := fs.Parse(args); err != nil {
		return err
	}

	nodePrivKey, nodePrivB64, nodePeerID, err := ensureNodeKey(*nodePriv)
	if err != nil {
		return err
	}

	fmt.Printf("NODE_PRIV_B64=%s\n", nodePrivB64)
	fmt.Printf("NODE_PEER_ID=%s\n", nodePeerID.String())

	if !*newSystem {
		return nil
	}

	sysPriv, sysPub, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		return err
	}
	sysPrivB64, err := marshalPrivB64(sysPriv)
	if err != nil {
		return err
	}
	sysPubB64, err := marshalPubB64(sysPub)
	if err != nil {
		return err
	}

	adminPriv, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		return err
	}
	adminPrivB64, err := marshalPrivB64(adminPriv)
	if err != nil {
		return err
	}
	adminPeerID, err := peer.IDFromPrivateKey(adminPriv)
	if err != nil {
		return err
	}

	validFrom := time.Now().UTC()
	validTo, err := parseTimeFlexible(*adminValidTo)
	if err != nil {
		return fmt.Errorf("admin-valid-to invalid: %w", err)
	}

	payload := []byte(fmt.Sprintf("%s|%s|admin|%s|%s",
		*clusterID,
		adminPeerID.String(),
		validFrom.UTC().Format(time.RFC3339Nano),
		validTo.UTC().Format(time.RFC3339Nano),
	))
	sig, err := sysPriv.Sign(payload)
	if err != nil {
		return err
	}
	sigB64 := base64.StdEncoding.EncodeToString(sig)

	fmt.Printf("SYSTEM_PRIV_B64=%s\n", sysPrivB64)
	fmt.Printf("SYSTEM_PUB_B64=%s\n", sysPubB64)
	fmt.Printf("ADMIN_PRIV_B64=%s\n", adminPrivB64)
	fmt.Printf("ADMIN_PEER_ID=%s\n", adminPeerID.String())
	fmt.Printf("ADMIN_PROOF_CLUSTER_ID=%s\n", *clusterID)
	fmt.Printf("ADMIN_PROOF_PEER_ID=%s\n", adminPeerID.String())
	fmt.Printf("ADMIN_PROOF_ROLE=admin\n")
	fmt.Printf("ADMIN_PROOF_VALID_FROM=%s\n", validFrom.UTC().Format(time.RFC3339Nano))
	fmt.Printf("ADMIN_PROOF_VALID_TO=%s\n", validTo.UTC().Format(time.RFC3339Nano))
	fmt.Printf("ADMIN_PROOF_SIG=%s\n", sigB64)

	_ = nodePrivKey
	return nil
}

func ensureNodeKey(nodePrivB64 string) (crypto.PrivKey, string, peer.ID, error) {
	if nodePrivB64 != "" {
		raw, err := base64.StdEncoding.DecodeString(nodePrivB64)
		if err != nil {
			return nil, "", "", err
		}
		priv, err := crypto.UnmarshalPrivateKey(raw)
		if err != nil {
			return nil, "", "", err
		}
		id, err := peer.IDFromPrivateKey(priv)
		if err != nil {
			return nil, "", "", err
		}
		return priv, nodePrivB64, id, nil
	}

	priv, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		return nil, "", "", err
	}
	b64, err := marshalPrivB64(priv)
	if err != nil {
		return nil, "", "", err
	}
	id, err := peer.IDFromPrivateKey(priv)
	if err != nil {
		return nil, "", "", err
	}
	return priv, b64, id, nil
}

func marshalPrivB64(priv crypto.PrivKey) (string, error) {
	raw, err := crypto.MarshalPrivateKey(priv)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func marshalPubB64(pub crypto.PubKey) (string, error) {
	raw, err := crypto.MarshalPublicKey(pub)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func parseTimeFlexible(raw string) (time.Time, error) {
	if ts, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return ts.UTC(), nil
	}
	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, err
	}
	return ts.UTC(), nil
}
