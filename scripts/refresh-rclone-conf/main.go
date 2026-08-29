// One-shot: rewrite data/rclone.conf from gdrive-token.json using rcloneprofile.
// Usage: HSERVER_DATA_DIR=/var/lib/hserver go run ./scripts/refresh-rclone-conf
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/IamYGT/heyserver/internal/rcloneprofile"
)

type tokenData struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	Expiry       time.Time `json:"expiry"`
}

func main() {
	dataDir := os.Getenv("HSERVER_DATA_DIR")
	if dataDir == "" {
		dataDir = "data"
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, "gdrive-token.json"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var td tokenData
	if err := json.Unmarshal(raw, &td); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	tok, _ := json.Marshal(td)
	cfg := rcloneprofile.RenderDriveRemoteConfig(rcloneprofile.RemoteName, string(tok), rcloneprofile.DefaultDriveTuning())
	out := filepath.Join(dataDir, "rclone.conf")
	if err := os.WriteFile(out, []byte(cfg), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote", out)
}
