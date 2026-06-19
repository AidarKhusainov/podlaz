package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	TransactionSchemaVersion = "podlaz.transaction.v1"
	TransactionOwner         = "podlaz"
	TransactionDirName       = "transactions"
	TransactionFileSuffix    = ".json"
)

// TransactionState describes the persisted lifecycle state for daemon-owned
// network and process mutations.
type TransactionState string

const (
	TransactionPlanned     TransactionState = "planned"
	TransactionApplying    TransactionState = "applying"
	TransactionApplied     TransactionState = "applied"
	TransactionVerifying   TransactionState = "verifying"
	TransactionCommitted   TransactionState = "committed"
	TransactionRollingBack TransactionState = "rolling_back"
	TransactionRolledBack  TransactionState = "rolled_back"
	TransactionFailed      TransactionState = "failed"
)

// Transaction is the versioned daemon runtime record written before any
// privileged full-tunnel mutation is applied.
type Transaction struct {
	SchemaVersion string           `json:"schema_version"`
	Owner         string           `json:"owner"`
	ID            string           `json:"id"`
	ProfileID     string           `json:"profile_id,omitempty"`
	Mode          string           `json:"mode,omitempty"`
	State         TransactionState `json:"state"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`

	BeforeSnapshot SnapshotMetadata  `json:"before_snapshot,omitempty"`
	DesiredPlan    DesiredPlan       `json:"desired_plan,omitempty"`
	AppliedSteps   []AppliedStep     `json:"applied_steps,omitempty"`
	Rollback       RollbackMetadata  `json:"rollback,omitempty"`
	Health         HealthResult      `json:"health_result,omitempty"`
	FailureReason  string            `json:"failure_reason,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
}

type SnapshotMetadata struct {
	CapturedAt time.Time `json:"captured_at,omitempty"`
	Source     string    `json:"source,omitempty"`
	Summary    []string  `json:"summary,omitempty"`
}

type DesiredPlan struct {
	PlanID string          `json:"plan_id,omitempty"`
	TUN    TUNDesiredState `json:"tun,omitempty"`
	Routes []RoutePlan     `json:"routes,omitempty"`
	DNS    DNSPlan         `json:"dns,omitempty"`
	NFT    NFTPlan         `json:"nftables,omitempty"`
	Core   CorePlan        `json:"core,omitempty"`
	Steps  []PlannedStep   `json:"steps,omitempty"`
}

type TUNDesiredState struct {
	InterfaceName string `json:"interface_name,omitempty"`
	MTU           int    `json:"mtu,omitempty"`
	Owner         string `json:"owner,omitempty"`
}

type RoutePlan struct {
	Kind      string `json:"kind,omitempty"`
	Table     string `json:"table,omitempty"`
	CIDR      string `json:"cidr,omitempty"`
	Via       string `json:"via,omitempty"`
	Dev       string `json:"dev,omitempty"`
	Priority  int    `json:"priority,omitempty"`
	Owner     string `json:"owner,omitempty"`
	Operation string `json:"operation,omitempty"`
}

type DNSPlan struct {
	Backend       string   `json:"backend,omitempty"`
	Link          string   `json:"link,omitempty"`
	Servers       []string `json:"servers,omitempty"`
	SearchDomains []string `json:"search_domains,omitempty"`
	Owner         string   `json:"owner,omitempty"`
}

type NFTPlan struct {
	Family string         `json:"family,omitempty"`
	Table  string         `json:"table,omitempty"`
	Chains []NFTChainPlan `json:"chains,omitempty"`
	Owner  string         `json:"owner,omitempty"`
}

type NFTChainPlan struct {
	Name     string   `json:"name,omitempty"`
	Hook     string   `json:"hook,omitempty"`
	Type     string   `json:"type,omitempty"`
	Priority int      `json:"priority,omitempty"`
	Policy   string   `json:"policy,omitempty"`
	Rules    []string `json:"rules,omitempty"`
	Owner    string   `json:"owner,omitempty"`
}

type CorePlan struct {
	RuntimeConfigPath string `json:"runtime_config_path,omitempty"`
	ProcessLabel      string `json:"process_label,omitempty"`
	Owner             string `json:"owner,omitempty"`
}

type PlannedStep struct {
	Kind        string `json:"kind"`
	Target      string `json:"target"`
	Description string `json:"description,omitempty"`
	Owner       string `json:"owner,omitempty"`
}

type AppliedStep struct {
	Kind        string    `json:"kind"`
	Target      string    `json:"target"`
	Description string    `json:"description,omitempty"`
	AppliedAt   time.Time `json:"applied_at"`
	Owner       string    `json:"owner,omitempty"`
}

type RollbackMetadata struct {
	TUN              []TUNRollback             `json:"tun,omitempty"`
	Routes           []RouteRollback           `json:"routes,omitempty"`
	PolicyRules      []PolicyRuleRollback      `json:"policy_rules,omitempty"`
	DNS              []DNSRollback             `json:"dns,omitempty"`
	NFTables         []NFTablesRollback        `json:"nftables,omitempty"`
	GeneratedConfigs []GeneratedConfigRollback `json:"generated_configs,omitempty"`
	ChildProcesses   []ChildProcessRollback    `json:"child_processes,omitempty"`
}

type TUNRollback struct {
	InterfaceName string `json:"interface_name"`
	Owner         string `json:"owner,omitempty"`
}

type RouteRollback struct {
	Table string `json:"table,omitempty"`
	CIDR  string `json:"cidr,omitempty"`
	Via   string `json:"via,omitempty"`
	Dev   string `json:"dev,omitempty"`
	Owner string `json:"owner,omitempty"`
}

type PolicyRuleRollback struct {
	Priority int    `json:"priority,omitempty"`
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	Table    string `json:"table,omitempty"`
	Mark     string `json:"mark,omitempty"`
	Owner    string `json:"owner,omitempty"`
}

type DNSRollback struct {
	Backend       string   `json:"backend,omitempty"`
	Link          string   `json:"link,omitempty"`
	Previous      []string `json:"previous,omitempty"`
	SearchDomains []string `json:"search_domains,omitempty"`
	Owner         string   `json:"owner,omitempty"`
}

type NFTablesRollback struct {
	Family string `json:"family,omitempty"`
	Table  string `json:"table,omitempty"`
	Chain  string `json:"chain,omitempty"`
	Owner  string `json:"owner,omitempty"`
}

type GeneratedConfigRollback struct {
	Path  string `json:"path"`
	Owner string `json:"owner,omitempty"`
}

type ChildProcessRollback struct {
	PID       int    `json:"pid,omitempty"`
	PidFile   string `json:"pid_file,omitempty"`
	Label     string `json:"label,omitempty"`
	ConfigRef string `json:"config_ref,omitempty"`
	Owner     string `json:"owner,omitempty"`
}

type HealthResult struct {
	Status    string    `json:"status,omitempty"`
	CheckedAt time.Time `json:"checked_at,omitempty"`
	Message   string    `json:"message,omitempty"`
}

type TransactionSummary struct {
	ID                string           `json:"id"`
	State             TransactionState `json:"state"`
	Path              string           `json:"path"`
	RollbackAvailable bool             `json:"rollback_available"`
	RequiresCleanup   bool             `json:"requires_cleanup"`
}

type TransactionStore struct {
	RuntimeDir string
	Now        func() time.Time
}

var (
	transactionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	secretValuePattern   = regexp.MustCompile(`(?i)(vless|vmess|trojan|ss)://|https?://[^\s?]+\?[^\s]+|\bbearer\s+[A-Za-z0-9._~+/-]+|\b(token|password|passwd|secret|api[_-]?key|authorization|private[_-]?key)=`)
	secretKeys           = map[string]struct{}{
		"token":               {},
		"access_token":        {},
		"refresh_token":       {},
		"password":            {},
		"passwd":              {},
		"secret":              {},
		"client_secret":       {},
		"api_key":             {},
		"apikey":              {},
		"authorization":       {},
		"private_key":         {},
		"privatekey":          {},
		"reality_private_key": {},
	}
	errEmptyTransactionID = errors.New("empty transaction id")
)

func NewTransaction(id, profileID, mode string, now time.Time) Transaction {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	return Transaction{
		SchemaVersion: TransactionSchemaVersion,
		Owner:         TransactionOwner,
		ID:            id,
		ProfileID:     profileID,
		Mode:          mode,
		State:         TransactionPlanned,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func (s TransactionStore) Path(id string) (string, error) {
	if err := validateTransactionID(id); err != nil {
		return "", err
	}
	runtimeDir := s.RuntimeDir
	if runtimeDir == "" {
		runtimeDir = "/run/podlaz"
	}
	return filepath.Join(runtimeDir, TransactionDirName, id+TransactionFileSuffix), nil
}

func (s TransactionStore) Save(tx Transaction) (string, error) {
	if tx.SchemaVersion == "" {
		tx.SchemaVersion = TransactionSchemaVersion
	}
	if tx.Owner == "" {
		tx.Owner = TransactionOwner
	}
	if tx.CreatedAt.IsZero() {
		tx.CreatedAt = s.now()
	}
	tx.UpdatedAt = s.now()
	if err := ValidateTransaction(tx); err != nil {
		return "", err
	}

	path, err := s.Path(tx.ID)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("create transaction directory %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(tx, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode transaction %s: %w", tx.ID, err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, "."+tx.ID+"-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create transaction temp file: %w", err)
	}
	tmpPath := tmp.Name()
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("set transaction temp permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write transaction temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("sync transaction temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close transaction temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("replace transaction file %s: %w", path, err)
	}
	removeTmp = false
	if err := syncDir(dir); err != nil {
		return "", fmt.Errorf("sync transaction directory %s: %w", dir, err)
	}
	return path, nil
}

func (s TransactionStore) Load(id string) (Transaction, string, error) {
	path, err := s.Path(id)
	if err != nil {
		return Transaction{}, "", err
	}
	tx, err := LoadTransactionFile(path)
	if err != nil {
		return Transaction{}, "", err
	}
	return tx, path, nil
}

func (s TransactionStore) Transition(id string, next TransactionState) (Transaction, string, error) {
	tx, path, err := s.Load(id)
	if err != nil {
		return Transaction{}, "", err
	}
	changed, err := Transition(&tx, next, s.now())
	if err != nil {
		return Transaction{}, "", err
	}
	if !changed {
		return tx, path, nil
	}
	path, err = s.Save(tx)
	return tx, path, err
}

func (s TransactionStore) Scan() ([]TransactionSummary, []string) {
	runtimeDir := s.RuntimeDir
	if runtimeDir == "" {
		runtimeDir = "/run/podlaz"
	}
	return ScanTransactions(runtimeDir)
}

func ScanTransactions(runtimeDir string) ([]TransactionSummary, []string) {
	dir := filepath.Join(runtimeDir, TransactionDirName)
	entries, err := os.ReadDir(dir)
	switch {
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
		return nil, nil
	default:
		return nil, []string{fmt.Sprintf("transaction directory %s: %v", dir, err)}
	}

	summaries := make([]TransactionSummary, 0, len(entries))
	warnings := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), TransactionFileSuffix) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		tx, err := LoadTransactionFile(path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("transaction file %s: %v", path, err))
			continue
		}
		summaries = append(summaries, tx.Summary(path))
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].State == summaries[j].State {
			return summaries[i].ID < summaries[j].ID
		}
		return summaries[i].State < summaries[j].State
	})
	sort.Strings(warnings)
	return summaries, warnings
}

func LoadTransactionFile(path string) (Transaction, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Transaction{}, err
	}
	var tx Transaction
	if err := json.Unmarshal(data, &tx); err != nil {
		return Transaction{}, fmt.Errorf("decode transaction JSON: %w", err)
	}
	if err := ValidateTransaction(tx); err != nil {
		return Transaction{}, err
	}
	return tx, nil
}

func ValidateTransaction(tx Transaction) error {
	if tx.SchemaVersion != TransactionSchemaVersion {
		return fmt.Errorf("unsupported transaction schema_version %q", tx.SchemaVersion)
	}
	if tx.Owner != TransactionOwner {
		return fmt.Errorf("unsupported transaction owner %q", tx.Owner)
	}
	if err := validateTransactionID(tx.ID); err != nil {
		return err
	}
	if !validTransactionState(tx.State) {
		return fmt.Errorf("invalid transaction state %q", tx.State)
	}
	if tx.CreatedAt.IsZero() {
		return errors.New("missing transaction created_at")
	}
	if tx.UpdatedAt.IsZero() {
		return errors.New("missing transaction updated_at")
	}
	if containsPersistentSecret(tx) {
		return errors.New("transaction contains data that looks like a persistent secret")
	}
	return nil
}

func Transition(tx *Transaction, next TransactionState, now time.Time) (bool, error) {
	if tx == nil {
		return false, errors.New("nil transaction")
	}
	if !validTransition(tx.State, next) {
		return false, fmt.Errorf("invalid transaction transition %s -> %s", tx.State, next)
	}
	if tx.State == next {
		return false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tx.State = next
	tx.UpdatedAt = now.UTC()
	return true, nil
}

func MarkFailure(tx *Transaction, reason string, now time.Time) (bool, error) {
	changed, err := Transition(tx, TransactionFailed, now)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(reason) != "" {
		tx.FailureReason = strings.TrimSpace(reason)
		if !changed {
			if now.IsZero() {
				now = time.Now().UTC()
			}
			tx.UpdatedAt = now.UTC()
		}
		return true, nil
	}
	return changed, nil
}

func (tx Transaction) Summary(path string) TransactionSummary {
	return TransactionSummary{
		ID:                tx.ID,
		State:             tx.State,
		Path:              path,
		RollbackAvailable: tx.Rollback.Available(),
		RequiresCleanup:   tx.RequiresCleanup(),
	}
}

func (tx Transaction) RequiresCleanup() bool {
	switch tx.State {
	case TransactionPlanned, TransactionApplying, TransactionApplied, TransactionVerifying, TransactionRollingBack, TransactionFailed:
		return true
	default:
		return false
	}
}

func (m RollbackMetadata) Available() bool {
	return len(m.TUN) > 0 || len(m.Routes) > 0 || len(m.PolicyRules) > 0 || len(m.DNS) > 0 || len(m.NFTables) > 0 || len(m.GeneratedConfigs) > 0 || len(m.ChildProcesses) > 0
}

func (s TransactionSummary) StatusLine() string {
	switch s.State {
	case TransactionPlanned:
		return "pending plan"
	case TransactionApplying:
		return "pending apply"
	case TransactionApplied, TransactionVerifying:
		return "pending verification"
	case TransactionCommitted:
		return "committed"
	case TransactionRollingBack:
		return "requires cleanup (rolling back)"
	case TransactionRolledBack:
		return "rolled back"
	case TransactionFailed:
		if s.RequiresCleanup {
			return "failed (requires cleanup)"
		}
		return "failed"
	default:
		return string(s.State)
	}
}

func (s TransactionSummary) RollbackLine() string {
	if s.RollbackAvailable {
		return "yes"
	}
	return "no"
}

func containsPersistentSecret(tx Transaction) bool {
	data, err := json.Marshal(tx)
	if err != nil {
		return true
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return true
	}
	return containsPersistentSecretValue("", value)
}

func containsPersistentSecretValue(key string, value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for childKey, childValue := range v {
			if containsPersistentSecretValue(childKey, childValue) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if containsPersistentSecretValue(key, child) {
				return true
			}
		}
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return false
		}
		if isSecretKey(key) {
			return true
		}
		return secretValuePattern.MatchString(trimmed)
	}
	return false
}

func isSecretKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), "-", "_"))
	_, ok := secretKeys[normalized]
	return ok
}

func validateTransactionID(id string) error {
	if strings.TrimSpace(id) == "" {
		return errEmptyTransactionID
	}
	if !transactionIDPattern.MatchString(id) {
		return fmt.Errorf("invalid transaction id %q", id)
	}
	return nil
}

func validTransactionState(state TransactionState) bool {
	switch state {
	case TransactionPlanned, TransactionApplying, TransactionApplied, TransactionVerifying, TransactionCommitted, TransactionRollingBack, TransactionRolledBack, TransactionFailed:
		return true
	default:
		return false
	}
}

func validTransition(from, to TransactionState) bool {
	if from == to {
		return validTransactionState(from)
	}
	switch from {
	case TransactionPlanned:
		return to == TransactionApplying || to == TransactionFailed || to == TransactionRollingBack
	case TransactionApplying:
		return to == TransactionApplied || to == TransactionRollingBack || to == TransactionFailed
	case TransactionApplied:
		return to == TransactionVerifying || to == TransactionRollingBack || to == TransactionFailed
	case TransactionVerifying:
		return to == TransactionCommitted || to == TransactionRollingBack || to == TransactionFailed
	case TransactionCommitted:
		return to == TransactionRollingBack || to == TransactionFailed
	case TransactionRollingBack:
		return to == TransactionRolledBack || to == TransactionFailed
	case TransactionFailed:
		return to == TransactionRollingBack
	case TransactionRolledBack:
		return false
	default:
		return false
	}
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (s TransactionStore) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
