package auth

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

const (
	authTimeout  = 5 * time.Second
	tokenLength  = 32
	saltLength   = 16
	AuthFile     = "sqlgo.auth"
	protoVersion = 1
)

type AuthMode string

const (
	ModeNone     AuthMode = "none"
	ModePassword AuthMode = "password"
	ModeToken    AuthMode = "token"
)

type Credential struct {
	Mode         AuthMode `json:"mode"`
	PasswordHash string   `json:"password_hash,omitempty"`
	Salt         string   `json:"salt,omitempty"`
	Tokens       []string `json:"tokens,omitempty"`
}

type HandshakeReq struct {
	Version  int    `json:"version"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
}

type HandshakeResp struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

func GenerateSalt() (string, error) {
	b := make([]byte, saltLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func GenerateToken() (string, error) {
	b := make([]byte, tokenLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func HashPassword(password, salt string) string {
	combined := salt + ":" + password
	sum := sha256.Sum256([]byte(combined))
	return hex.EncodeToString(sum[:])
}

func NewPasswordCredential(password string) (*Credential, error) {
	salt, err := GenerateSalt()
	if err != nil {
		return nil, err
	}
	return &Credential{
		Mode:         ModePassword,
		PasswordHash: HashPassword(password, salt),
		Salt:         salt,
	}, nil
}

func NewTokenCredential(tokens []string) *Credential {
	hashed := make([]string, len(tokens))
	for i, t := range tokens {
		sum := sha256.Sum256([]byte(t))
		hashed[i] = hex.EncodeToString(sum[:])
	}
	return &Credential{
		Mode:   ModeToken,
		Tokens: hashed,
	}
}

func (c *Credential) CheckPassword(password string) bool {
	if c.Mode != ModePassword {
		return false
	}
	want := HashPassword(password, c.Salt)
	return subtle.ConstantTimeCompare([]byte(want), []byte(c.PasswordHash)) == 1
}

func (c *Credential) CheckToken(token string) bool {
	if c.Mode != ModeToken {
		return false
	}
	sum := sha256.Sum256([]byte(token))
	got := hex.EncodeToString(sum[:])
	for _, t := range c.Tokens {
		if subtle.ConstantTimeCompare([]byte(got), []byte(t)) == 1 {
			return true
		}
	}
	return false
}

func (c *Credential) Check(req *HandshakeReq) bool {
	switch c.Mode {
	case ModeNone:
		return true
	case ModePassword:
		return c.CheckPassword(req.Password)
	case ModeToken:
		return c.CheckToken(req.Token)
	}
	return false
}

func SaveCredential(path string, cred *Credential) error {
	data, err := json.MarshalIndent(cred, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func LoadCredential(path string) (*Credential, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Credential{Mode: ModeNone}, nil
		}
		return nil, err
	}
	var cred Credential
	if err := json.Unmarshal(data, &cred); err != nil {
		return nil, fmt.Errorf("malformed auth file: %w", err)
	}
	return &cred, nil
}

func ServerHandshake(conn net.Conn, cred *Credential) error {
	if cred.Mode == ModeNone {
		return nil
	}

	conn.SetDeadline(time.Now().Add(authTimeout))
	defer conn.SetDeadline(time.Time{})

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), 4096)
	enc := json.NewEncoder(conn)

	challenge := map[string]interface{}{
		"version": protoVersion,
		"mode":    string(cred.Mode),
		"require": true,
	}
	if err := enc.Encode(challenge); err != nil {
		return fmt.Errorf("auth challenge send failed: %w", err)
	}

	if !scanner.Scan() {
		return fmt.Errorf("client disconnected during auth")
	}

	var req HandshakeReq
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		sendAuthResp(enc, false, "invalid auth payload")
		return fmt.Errorf("auth parse error: %w", err)
	}

	if !cred.Check(&req) {
		sendAuthResp(enc, false, "authentication failed")
		return fmt.Errorf("authentication failed for connection from %s", conn.RemoteAddr())
	}

	return sendAuthResp(enc, true, "authenticated")
}

func ClientHandshake(conn net.Conn, password, token string) error {
	conn.SetDeadline(time.Now().Add(authTimeout))
	defer conn.SetDeadline(time.Time{})

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), 4096)
	enc := json.NewEncoder(conn)

	if !scanner.Scan() {
		return fmt.Errorf("server disconnected before auth challenge")
	}

	var challenge map[string]interface{}
	if err := json.Unmarshal(scanner.Bytes(), &challenge); err != nil {
		return fmt.Errorf("bad auth challenge: %w", err)
	}

	require, _ := challenge["require"].(bool)
	if !require {
		return nil
	}

	req := HandshakeReq{
		Version:  protoVersion,
		Password: password,
		Token:    token,
	}
	if err := enc.Encode(req); err != nil {
		return fmt.Errorf("auth send failed: %w", err)
	}

	if !scanner.Scan() {
		return fmt.Errorf("server disconnected after auth send")
	}

	var resp HandshakeResp
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return fmt.Errorf("auth response parse error: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("auth rejected: %s", resp.Message)
	}
	return nil
}

func sendAuthResp(enc *json.Encoder, ok bool, msg string) error {
	return enc.Encode(HandshakeResp{OK: ok, Message: msg})
}

func InitAuthInteractive(authFile string) error {
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Auth mode [none/password/token]: ")
	mode, _ := reader.ReadString('\n')
	mode = strings.TrimSpace(strings.ToLower(mode))

	switch AuthMode(mode) {
	case ModeNone:
		cred := &Credential{Mode: ModeNone}
		if err := SaveCredential(authFile, cred); err != nil {
			return err
		}
		fmt.Println("Auth disabled.")

	case ModePassword:
		fmt.Print("Password: ")
		pw, _ := reader.ReadString('\n')
		pw = strings.TrimSpace(pw)
		if pw == "" {
			return fmt.Errorf("password cannot be empty")
		}
		cred, err := NewPasswordCredential(pw)
		if err != nil {
			return err
		}
		if err := SaveCredential(authFile, cred); err != nil {
			return err
		}
		fmt.Printf("Password auth saved to %s\n", authFile)

	case ModeToken:
		fmt.Print("Number of tokens to generate [1]: ")
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		n := 1
		if line != "" {
			fmt.Sscanf(line, "%d", &n)
		}
		tokens := make([]string, n)
		for i := 0; i < n; i++ {
			t, err := GenerateToken()
			if err != nil {
				return err
			}
			tokens[i] = t
			fmt.Printf("Token %d: %s\n", i+1, t)
		}
		cred := NewTokenCredential(tokens)
		if err := SaveCredential(authFile, cred); err != nil {
			return err
		}
		fmt.Printf("Token auth saved to %s\n", authFile)

	default:
		return fmt.Errorf("unknown auth mode: %s", mode)
	}
	return nil
}
