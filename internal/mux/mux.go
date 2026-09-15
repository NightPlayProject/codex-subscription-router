package mux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/b-nnett/codex-subscription-router/internal/backend"
	"github.com/b-nnett/codex-subscription-router/internal/protocol"
	"github.com/b-nnett/codex-subscription-router/internal/state"
)

const requestTimeout = 30 * time.Second

type Options struct {
	RealExecutable string
	RealArgs       []string
	Environment    []string
	Store          *state.Store
	Output         io.Writer
}

type externalRoute struct {
	accountID  string
	method     string
	message    protocol.Message
	excluded   map[string]struct{}
	chatGPTWeb bool
}

type serverRequestRoute struct {
	accountID string
	original  json.RawMessage
}

type Event struct {
	Type      string `json:"type"`
	AccountID string `json:"accountId,omitempty"`
	Message   string `json:"message,omitempty"`
	Data      any    `json:"data,omitempty"`
}

// Multiplexer presents one app-server connection to ChatGPT.app while owning
// one real app-server process per ChatGPT subscription.
type Multiplexer struct {
	realExecutable string
	realArgs       []string
	environment    []string
	store          *state.Store
	output         io.Writer

	childrenMu sync.RWMutex
	children   map[string]*backend.Child
	inbound    chan backend.Inbound

	initializationMu sync.RWMutex
	initializeParams json.RawMessage
	initialized      bool

	externalMu     sync.Mutex
	externalRoutes map[string]externalRoute
	serverMu       sync.Mutex
	serverRoutes   map[string]serverRequestRoute
	serverSequence atomic.Uint64

	outputMu sync.Mutex
	eventsMu sync.RWMutex
	events   map[chan Event]struct{}

	threadRouteMu       sync.Mutex
	threadRouteLocks    map[string]*sync.Mutex
	threadModelMu       sync.RWMutex
	threadModelFamilies map[string]bool
	migrationMu         sync.RWMutex
	migrating           map[string]bool
	batchMu             sync.Mutex
	batchStatus         RoutingStatus
	batchGeneration     uint64
	goalExclusions      map[string]map[string]time.Time

	profileMu     sync.Mutex
	profileClient *http.Client
	profileCache  map[string]profileCacheEntry
	now           func() time.Time

	resetCreditsMu       sync.Mutex
	resetCreditsCache    map[string]resetCreditsCacheEntry
	resetCreditsEndpoint string

	previewMu        sync.RWMutex
	rateLimitPreview *RateLimitPreview

	resetPreviewMu sync.RWMutex
	resetPreviews  map[string]ResetCreditsPreview
}

func New(options Options) (*Multiplexer, error) {
	if options.RealExecutable == "" || options.Store == nil || options.Output == nil {
		return nil, errors.New("real executable, store, and output are required")
	}
	return &Multiplexer{
		realExecutable:       options.RealExecutable,
		realArgs:             withImmediateThreadUnload(options.RealArgs),
		environment:          append([]string(nil), options.Environment...),
		store:                options.Store,
		output:               options.Output,
		children:             make(map[string]*backend.Child),
		inbound:              make(chan backend.Inbound, 1024),
		externalRoutes:       make(map[string]externalRoute),
		serverRoutes:         make(map[string]serverRequestRoute),
		events:               make(map[chan Event]struct{}),
		threadRouteLocks:     make(map[string]*sync.Mutex),
		threadModelFamilies:  make(map[string]bool),
		profileClient:        &http.Client{Timeout: 10 * time.Second},
		profileCache:         make(map[string]profileCacheEntry),
		now:                  time.Now,
		resetCreditsCache:    make(map[string]resetCreditsCacheEntry),
		resetCreditsEndpoint: rateLimitResetCreditsURL,
		resetPreviews:        make(map[string]ResetCreditsPreview),
	}, nil
}

func (m *Multiplexer) Start(ctx context.Context) error {
	for _, account := range m.store.Accounts() {
		if _, err := m.startChild(ctx, account); err != nil {
			fmt.Fprintf(os.Stderr, "codex-mux: start account %s: %v\n", account.ID, err)
		}
	}
	if len(m.childEntries()) == 0 {
		return errors.New("no Codex app-server process could be started")
	}
	go m.inboundLoop(ctx)
	go m.syncManagedConfigLoop(ctx)
	return nil
}

func (m *Multiplexer) syncManagedConfigLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.store.SyncManagedConfig(); err != nil {
				fmt.Fprintf(os.Stderr, "codex-mux: sync shared plugin config: %v\n", err)
			}
		}
	}
}

func (m *Multiplexer) Close() {
	m.batchMu.Lock()
	m.batchGeneration++
	m.batchMu.Unlock()
	for _, entry := range m.childEntries() {
		_ = entry.child.Close()
	}
}

func (m *Multiplexer) HandleClient(message protocol.Message) {
	if message.Method == "" && len(message.ID) > 0 {
		m.handleServerRequestResponse(message)
		return
	}
	if message.Method == "initialize" && len(message.ID) > 0 {
		go m.initialize(message)
		return
	}
	if len(message.ID) == 0 {
		m.handleClientNotification(message)
		return
	}

	switch message.Method {
	case "thread/list":
		go m.aggregateThreadList(message)
	case "thread/start":
		go m.routeNewThread(message)
	case "account/rateLimits/read":
		go m.routeAggregatedRateLimits(message)
	default:
		m.routeExistingRequest(message)
	}
}

func (m *Multiplexer) initialize(message protocol.Message) {
	m.initializationMu.Lock()
	m.initializeParams = append(json.RawMessage(nil), message.Params...)
	m.initializationMu.Unlock()

	var firstResult json.RawMessage
	var firstErr error
	for _, entry := range m.childEntries() {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		response, err := entry.child.Request(ctx, "initialize", message.Params)
		cancel()
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if firstResult == nil {
			firstResult = response.Result
		}
	}
	if firstResult == nil {
		m.write(protocol.Failure(message.ID, -32000, fmt.Sprintf("failed to initialize account pool: %v", firstErr)))
		return
	}
	m.write(protocol.Success(message.ID, firstResult))
}

func (m *Multiplexer) handleClientNotification(message protocol.Message) {
	if message.Method == "initialized" {
		m.initializationMu.Lock()
		m.initialized = true
		m.initializationMu.Unlock()
		for _, entry := range m.childEntries() {
			_ = entry.child.Send(message)
		}
		return
	}
	if controller, ok := m.controllerChild(); ok {
		_ = controller.Send(message)
	}
}

func (m *Multiplexer) routeNewThread(message protocol.Message) {
	if m.requestUsesChatGPTWeb(message, "", "") {
		controller, ok := m.store.Controller()
		if !ok {
			m.write(protocol.Failure(message.ID, -32022, "no controller account is configured"))
			return
		}
		if err := m.forward(controller.ID, message); err != nil {
			m.write(protocol.Failure(message.ID, -32023, err.Error()))
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	account, reason, err := m.chooseAccount(ctx)
	if err != nil {
		if errors.Is(err, errNoSubscriptionCapacity) {
			m.write(m.allSubscriptionsDepleted(ctx, message.ID))
			return
		}
		m.write(protocol.Failure(message.ID, -32020, err.Error()))
		return
	}
	if err := m.forward(account.ID, message); err != nil {
		m.write(protocol.Failure(message.ID, -32021, err.Error()))
		return
	}
	m.publish(Event{
		Type:      "thread-routed",
		AccountID: account.ID,
		Message:   fmt.Sprintf("New chat pinned to %s", account.Label),
		Data:      reason,
	})
}

func (m *Multiplexer) routeExistingRequest(message protocol.Message) {
	accountID := ""
	if scopedAccountID, cleanedParams, ok := scopedPluginRequest(message.Method, message.Params); ok {
		if account, exists := m.store.Account(scopedAccountID); exists && account.Enabled {
			message.Params = cleanedParams
			if err := m.forward(scopedAccountID, message); err != nil {
				m.write(protocol.Failure(message.ID, -32023, err.Error()))
			}
			return
		}
	}
	threadID := threadIDFromParams(message.Params)
	if threadID != "" {
		accountID, _ = m.store.ThreadOwner(threadID)
	}
	if accountID == "" {
		if controller, ok := m.store.Controller(); ok {
			accountID = controller.ID
		}
	}
	if accountID == "" {
		m.write(protocol.Failure(message.ID, -32022, "no controller account is configured"))
		return
	}
	if threadID != "" && m.keepChatGPTWebThreadOnCurrentAccount(message, threadID, accountID) {
		if err := m.forward(accountID, message); err != nil {
			m.write(protocol.Failure(message.ID, -32023, err.Error()))
		}
		return
	}
	if (message.Method == "turn/start" || startsGoal(message)) && threadID != "" {
		go m.routeTurnStart(message, threadID, accountID)
		return
	}
	if threadID != "" {
		go func() {
			unlock := m.lockThreadRoute(threadID)
			defer unlock()
			if owner, ok := m.store.ThreadOwner(threadID); ok {
				accountID = owner
			}
			if message.Method == "thread/resume" {
				if preferred, ok := m.preferredThreadAccount(accountID); ok {
					ctx, cancel := context.WithTimeout(context.Background(), 2*requestTimeout)
					err := m.moveThreadToAccountForRequestWithOptions(ctx, threadID, accountID, preferred.ID, message, true)
					cancel()
					if err != nil && !errors.Is(err, errChatActive) {
						m.publish(Event{
							Type:      "routing-preference-unavailable",
							AccountID: preferred.ID,
							Message:   fmt.Sprintf("Could not move chat to %s; opening its current subscription: %v", preferred.Label, err),
							Data:      map[string]any{"threadId": threadID, "currentAccountId": accountID},
						})
					}
					if err == nil {
						accountID = preferred.ID
					}
					// The UI's old path belongs to the previous account. The
					// migration already resumed a validated target-local path.
					var params map[string]json.RawMessage
					if json.Unmarshal(message.Params, &params) == nil {
						delete(params, "path")
						delete(params, "history")
						message.Params, _ = json.Marshal(params)
					}
				}
				var params map[string]json.RawMessage
				if json.Unmarshal(message.Params, &params) == nil {
					var path string
					_ = json.Unmarshal(params["path"], &path)
					if account, ok := m.store.Account(accountID); ok && path != "" {
						if _, err := relativePathInside(account.CodexHome, path); err != nil {
							delete(params, "path")
							message.Params, _ = json.Marshal(params)
						}
					}
				}
			}
			if err := m.forward(accountID, message); err != nil {
				m.write(protocol.Failure(message.ID, -32023, err.Error()))
			}
		}()
		return
	}
	if err := m.forward(accountID, message); err != nil {
		m.write(protocol.Failure(message.ID, -32023, err.Error()))
	}
}

func (m *Multiplexer) forward(accountID string, message protocol.Message) error {
	return m.forwardWithExclusions(accountID, message, nil)
}

func (m *Multiplexer) forwardWithExclusions(accountID string, message protocol.Message, excluded map[string]struct{}) error {
	child, ok := m.child(accountID)
	if !ok {
		return fmt.Errorf("account %s is unavailable", accountID)
	}
	key := protocol.RequestIDKey(message.ID)
	m.externalMu.Lock()
	m.externalRoutes[key] = externalRoute{
		accountID:  accountID,
		method:     message.Method,
		message:    message,
		excluded:   cloneAccountSet(excluded),
		chatGPTWeb: m.requestUsesChatGPTWeb(message, threadIDFromParams(message.Params), accountID),
	}
	m.externalMu.Unlock()
	if err := child.Send(message); err != nil {
		m.externalMu.Lock()
		delete(m.externalRoutes, key)
		m.externalMu.Unlock()
		return err
	}
	return nil
}

func (m *Multiplexer) routeAggregatedRateLimits(message protocol.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	rateLimits, err := m.AggregatedRateLimits(ctx)
	if err != nil {
		m.write(protocol.Failure(message.ID, -32024, err.Error()))
		return
	}
	result, err := json.Marshal(map[string]any{"rateLimits": rateLimits})
	if err != nil {
		m.write(protocol.Failure(message.ID, -32025, err.Error()))
		return
	}
	m.write(protocol.Success(message.ID, result))
}

func (m *Multiplexer) routeTurnStart(message protocol.Message, threadID, ownerID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*requestTimeout)
	defer cancel()
	unlock := m.lockThreadRoute(threadID)
	defer unlock()

	// Re-read ownership after taking the per-thread lock. Another turn may have
	// migrated the thread while this request was waiting.
	if currentOwnerID, ok := m.store.ThreadOwner(threadID); ok {
		ownerID = currentOwnerID
	}
	if preferred, ok := m.preferredThreadAccount(ownerID); ok {
		snapshot, err := m.accountSnapshotWithProfile(ctx, preferred.ID, false)
		if err == nil && accountHasCapacity(snapshot) {
			if err := m.moveThreadToAccountForRequest(ctx, threadID, ownerID, preferred.ID, message); err != nil {
				if errors.Is(err, errChatActive) {
					if err := m.forward(ownerID, message); err != nil {
						m.write(protocol.Failure(message.ID, -32023, err.Error()))
					}
					return
				}
				m.write(protocol.Failure(message.ID, -32027, fmt.Sprintf("move chat to %s: %v", preferred.Label, err)))
				return
			}
			if err := m.forward(preferred.ID, message); err != nil {
				m.write(protocol.Failure(message.ID, -32023, err.Error()))
				return
			}
			m.publish(Event{
				Type:      "thread-subscription-changed",
				AccountID: preferred.ID,
				Message:   fmt.Sprintf("Chat moved to %s", preferred.Label),
				Data:      map[string]any{"threadId": threadID, "previousAccountId": ownerID},
			})
			return
		}
		m.publish(Event{
			Type:      "routing-preference-unavailable",
			AccountID: preferred.ID,
			Message:   fmt.Sprintf("%s is unavailable or out of capacity; this chat kept its current subscription", preferred.Label),
			Data:      map[string]any{"threadId": threadID, "currentAccountId": ownerID},
		})
	}
	snapshot, err := m.accountSnapshotWithProfile(ctx, ownerID, false)
	if err != nil || accountHasCapacity(snapshot) {
		if err := m.forward(ownerID, message); err != nil {
			m.write(protocol.Failure(message.ID, -32023, err.Error()))
		}
		return
	}
	excluded := map[string]struct{}{ownerID: {}}
	m.failoverTurn(ctx, message, threadID, ownerID, excluded, false)
}

func (m *Multiplexer) preferredThreadAccount(ownerID string) (state.Account, bool) {
	preferredID := m.store.PreferredNewThreadAccountID()
	if preferredID == "" || preferredID == ownerID {
		return state.Account{}, false
	}
	account, ok := m.store.Account(preferredID)
	if !ok || !account.Enabled {
		return state.Account{}, false
	}
	return account, true
}

func (m *Multiplexer) keepChatGPTWebThreadOnCurrentAccount(message protocol.Message, threadID, ownerID string) bool {
	if !m.requestUsesChatGPTWeb(message, threadID, ownerID) {
		return false
	}
	if message.Method == "thread/resume" {
		if _, ok := m.preferredThreadAccount(ownerID); ok {
			return false
		}
	}
	return true
}

func (m *Multiplexer) lockThreadRoute(threadID string) func() {
	m.threadRouteMu.Lock()
	if m.threadRouteLocks == nil {
		m.threadRouteLocks = make(map[string]*sync.Mutex)
	}
	lock := m.threadRouteLocks[threadID]
	if lock == nil {
		lock = &sync.Mutex{}
		m.threadRouteLocks[threadID] = lock
	}
	m.threadRouteMu.Unlock()
	lock.Lock()
	return lock.Unlock
}

func (m *Multiplexer) failoverTurn(
	ctx context.Context,
	message protocol.Message,
	threadID string,
	sourceAccountID string,
	excluded map[string]struct{},
	waitForSourceIdle bool,
) {
	fallback, _, err := m.chooseAccountExcluding(ctx, excluded)
	if err != nil {
		m.write(m.allSubscriptionsDepleted(ctx, message.ID))
		return
	}
	if err := m.moveThreadToAccountForRequestWithTransition(ctx, threadID, sourceAccountID, fallback.ID, message, waitForSourceIdle); err != nil {
		m.write(protocol.Failure(message.ID, -32027, fmt.Sprintf("move chat to %s: %v", fallback.Label, err)))
		return
	}
	if err := m.forwardWithExclusions(fallback.ID, message, excluded); err != nil {
		m.write(protocol.Failure(message.ID, -32023, err.Error()))
		return
	}
	m.publish(Event{
		Type:      "thread-failed-over",
		AccountID: fallback.ID,
		Message:   fmt.Sprintf("Chat continued with %s", fallback.Label),
		Data:      map[string]any{"threadId": threadID, "previousAccountId": sourceAccountID},
	})
}

func (m *Multiplexer) moveThreadToAccount(ctx context.Context, threadID, sourceAccountID, targetAccountID string) error {
	return m.moveThreadWithGoal(ctx, threadID, sourceAccountID, targetAccountID)
}

func (m *Multiplexer) moveThreadToAccountForRequest(
	ctx context.Context,
	threadID string,
	sourceAccountID string,
	targetAccountID string,
	message protocol.Message,
) error {
	return m.moveThreadToAccountForRequestWithOptions(ctx, threadID, sourceAccountID, targetAccountID, message, false)
}

func (m *Multiplexer) moveThreadToAccountForRequestWithOptions(
	ctx context.Context,
	threadID string,
	sourceAccountID string,
	targetAccountID string,
	message protocol.Message,
	manualResume bool,
) error {
	return m.moveThreadToAccountForRequestWithTransitionOptions(ctx, threadID, sourceAccountID, targetAccountID, message, false, manualResume)
}

func (m *Multiplexer) moveThreadToAccountForRequestWithTransition(
	ctx context.Context,
	threadID string,
	sourceAccountID string,
	targetAccountID string,
	message protocol.Message,
	waitForSourceIdle bool,
) error {
	return m.moveThreadToAccountForRequestWithTransitionOptions(ctx, threadID, sourceAccountID, targetAccountID, message, waitForSourceIdle, false)
}

func (m *Multiplexer) moveThreadToAccountForRequestWithTransitionOptions(
	ctx context.Context,
	threadID string,
	sourceAccountID string,
	targetAccountID string,
	message protocol.Message,
	waitForSourceIdle bool,
	manualResume bool,
) error {
	allowChatGPTWebSource := requestAllowsChatGPTWebSourceMigration(message, manualResume)
	return m.moveThreadWithGoalMigrationOptions(ctx, threadID, sourceAccountID, targetAccountID, allowChatGPTWebSource, waitForSourceIdle)
}

func requestAllowsChatGPTWebSourceMigration(message protocol.Message, manualResume bool) bool {
	return requestExplicitlyUsesNativeModel(message) || (manualResume && message.Method == "thread/resume")
}

func (m *Multiplexer) moveThreadToAccountWithResume(
	ctx context.Context,
	threadID string,
	sourceAccountID string,
	targetAccountID string,
	resume func(context.Context, string, string, string) error,
) error {
	if sourceAccountID == targetAccountID {
		return nil
	}
	if err := resume(ctx, threadID, sourceAccountID, targetAccountID); err != nil {
		return err
	}
	if err := m.store.SetThreadOwner(threadID, targetAccountID); err != nil {
		return fmt.Errorf("persist chat subscription: %w", err)
	}
	return nil
}

func (m *Multiplexer) resumeThreadOnAccount(ctx context.Context, threadID, sourceAccountID, targetAccountID string) error {
	return m.resumeThreadOnAccountWithOptions(ctx, threadID, sourceAccountID, targetAccountID, false, false)
}

func (m *Multiplexer) resumeThreadOnAccountAllowChatGPTWebSource(ctx context.Context, threadID, sourceAccountID, targetAccountID string) error {
	return m.resumeThreadOnAccountWithOptions(ctx, threadID, sourceAccountID, targetAccountID, true, false)
}

func (m *Multiplexer) resumeThreadOnAccountWithOptions(ctx context.Context, threadID, sourceAccountID, targetAccountID string, allowChatGPTWebSource, waitForSourceIdle bool) error {
	m.migrationMu.Lock()
	if m.migrating == nil {
		m.migrating = make(map[string]bool)
	}
	m.migrating[threadID] = true
	m.migrationMu.Unlock()
	defer func() { m.migrationMu.Lock(); delete(m.migrating, threadID); m.migrationMu.Unlock() }()
	sourceAccount, ok := m.store.Account(sourceAccountID)
	if !ok {
		return fmt.Errorf("source subscription metadata is unavailable")
	}
	targetAccount, ok := m.store.Account(targetAccountID)
	if !ok {
		return fmt.Errorf("target subscription metadata is unavailable")
	}
	source, ok := m.child(sourceAccountID)
	if !ok {
		return fmt.Errorf("source subscription is unavailable")
	}
	target, ok := m.child(targetAccountID)
	if !ok {
		return fmt.Errorf("target subscription is unavailable")
	}
	if err := refreshSignedInSubscription(ctx, target); err != nil {
		return err
	}
	return resumeThreadBetweenAccountsWithOptions(
		ctx,
		threadID,
		sourceAccount.CodexHome,
		targetAccount.CodexHome,
		source,
		target,
		threadMigrationOptions{allowChatGPTWebSource: allowChatGPTWebSource, waitForSourceIdle: waitForSourceIdle},
	)
}

func (m *Multiplexer) handleServerRequestResponse(message protocol.Message) {
	key := protocol.RequestIDKey(message.ID)
	m.serverMu.Lock()
	route, ok := m.serverRoutes[key]
	if ok {
		delete(m.serverRoutes, key)
	}
	m.serverMu.Unlock()
	if !ok {
		return
	}
	message.ID = route.original
	if child, exists := m.child(route.accountID); exists {
		_ = child.Send(message)
	}
}

func (m *Multiplexer) inboundLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case inbound := <-m.inbound:
			m.handleInbound(inbound)
		}
	}
}

func (m *Multiplexer) handleInbound(inbound backend.Inbound) {
	message := inbound.Message
	if len(message.ID) == 0 && strings.HasPrefix(message.Method, "thread/") {
		id := threadIDFromNotification(message.Params)
		m.migrationMu.RLock()
		internal := m.migrating[id]
		m.migrationMu.RUnlock()
		if internal {
			return
		}
		if owner, known := m.store.ThreadOwner(id); known && owner != inbound.AccountID {
			// An old account may close its idle copy after migration commits.
			return
		}
	}
	if message.Method == "" && len(message.ID) > 0 {
		key := protocol.RequestIDKey(message.ID)
		m.externalMu.Lock()
		route, ok := m.externalRoutes[key]
		if ok {
			delete(m.externalRoutes, key)
		}
		m.externalMu.Unlock()
		if ok {
			if !route.chatGPTWeb && (route.method == "turn/start" || startsGoal(route.message)) && isUsageLimitResponse(message) {
				go m.retryTurnAfterUsageLimit(route, inbound.AccountID)
				return
			}
			m.learnThreadOwner(route, inbound.AccountID, message.Result)
			m.writeRaw(inbound.Raw)
		}
		return
	}
	if message.Method != "" && len(message.ID) > 0 {
		m.forwardServerRequest(inbound)
		return
	}
	if message.Method == "thread/goal/updated" {
		var params struct {
			ThreadID string `json:"threadId"`
			Goal     struct {
				Status string `json:"status"`
			} `json:"goal"`
		}
		if json.Unmarshal(message.Params, &params) == nil && params.ThreadID != "" && params.Goal.Status == "usageLimited" {
			go func() {
				if !m.routeLimitedGoal(inbound.AccountID, params.ThreadID) {
					m.writeRaw(inbound.Raw)
				}
			}()
			return
		}
	}
	if message.Method == "account/rateLimits/updated" {
		go m.forwardAggregatedRateLimitNotification(inbound.Raw)
		return
	}
	if message.Method == "thread/started" {
		if threadID := threadIDFromNotification(message.Params); threadID != "" {
			_ = m.store.LearnThreadOwner(threadID, inbound.AccountID)
		}
	}
	if message.Method == "turn/completed" {
		go m.routeGoalAtTurnBoundary(inbound.AccountID, threadIDFromNotification(message.Params))
	}
	if message.Method == "turn/completed" ||
		message.Method == "account/login/completed" ||
		message.Method == "account/updated" {
		go m.publishAccountRefresh(inbound.AccountID)
	}
	if m.shouldForwardNotification(inbound.AccountID, message.Method) {
		m.writeRaw(inbound.Raw)
	}
}

func (m *Multiplexer) forwardAggregatedRateLimitNotification(fallback []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	rateLimits, err := m.AggregatedRateLimits(ctx)
	if err != nil {
		m.writeRaw(fallback)
		return
	}
	params, err := json.Marshal(map[string]any{"rateLimits": rateLimits})
	if err != nil {
		m.writeRaw(fallback)
		return
	}
	m.write(protocol.Message{Method: "account/rateLimits/updated", Params: params})
}

func (m *Multiplexer) retryTurnAfterUsageLimit(route externalRoute, exhaustedAccountID string) {
	threadID := threadIDFromParams(route.message.Params)
	if threadID == "" {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		m.write(m.allSubscriptionsDepleted(ctx, route.message.ID))
		return
	}
	excluded := cloneAccountSet(route.excluded)
	if excluded == nil {
		excluded = make(map[string]struct{})
	}
	excluded[exhaustedAccountID] = struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*requestTimeout)
	defer cancel()
	m.failoverTurn(ctx, route.message, threadID, exhaustedAccountID, excluded, true)
}

func (m *Multiplexer) forwardServerRequest(inbound backend.Inbound) {
	sequence := m.serverSequence.Add(1)
	newID := protocol.StringID(fmt.Sprintf("codex-mux:%s:%d", inbound.AccountID, sequence))
	key := protocol.RequestIDKey(newID)
	m.serverMu.Lock()
	m.serverRoutes[key] = serverRequestRoute{
		accountID: inbound.AccountID,
		original:  append(json.RawMessage(nil), inbound.Message.ID...),
	}
	m.serverMu.Unlock()
	inbound.Message.ID = newID
	m.write(inbound.Message)
}

func (m *Multiplexer) shouldForwardNotification(accountID, method string) bool {
	controller, ok := m.store.Controller()
	if ok && controller.ID == accountID {
		return true
	}
	return strings.HasPrefix(method, "thread/") ||
		strings.HasPrefix(method, "turn/") ||
		strings.HasPrefix(method, "item/") ||
		strings.HasPrefix(method, "hook/") ||
		strings.HasPrefix(method, "rawResponse")
}

func (m *Multiplexer) learnThreadOwner(route externalRoute, accountID string, result json.RawMessage) {
	switch route.method {
	case "thread/start", "thread/fork", "thread/resume", "thread/unarchive":
		if threadID := threadIDFromResult(result); threadID != "" {
			m.rememberThreadModelFamily(threadID, route.chatGPTWeb)
			_ = m.store.LearnThreadOwner(threadID, accountID)
		}
	}
}

func (m *Multiplexer) write(message protocol.Message) {
	encoded, err := protocol.Encode(message)
	if err != nil {
		fmt.Fprintf(os.Stderr, "codex-mux: encode response: %v\n", err)
		return
	}
	m.writeRaw(encoded)
}

func (m *Multiplexer) writeRaw(encoded []byte) {
	m.outputMu.Lock()
	defer m.outputMu.Unlock()
	_, _ = m.output.Write(append(encoded, '\n'))
}

type childEntry struct {
	account state.Account
	child   *backend.Child
}

func (m *Multiplexer) childEntries() []childEntry {
	accounts := m.store.Accounts()
	m.childrenMu.RLock()
	defer m.childrenMu.RUnlock()
	entries := make([]childEntry, 0, len(accounts))
	for _, account := range accounts {
		if child := m.children[account.ID]; child != nil {
			entries = append(entries, childEntry{account: account, child: child})
		}
	}
	return entries
}

func (m *Multiplexer) child(accountID string) (*backend.Child, bool) {
	m.childrenMu.RLock()
	defer m.childrenMu.RUnlock()
	child, ok := m.children[accountID]
	return child, ok
}

func (m *Multiplexer) controllerChild() (*backend.Child, bool) {
	controller, ok := m.store.Controller()
	if !ok {
		return nil, false
	}
	return m.child(controller.ID)
}

func (m *Multiplexer) startChild(ctx context.Context, account state.Account) (*backend.Child, error) {
	if child, ok := m.child(account.ID); ok {
		return child, nil
	}
	child, err := backend.Start(
		account.ID,
		account.CodexHome,
		m.realExecutable,
		m.realArgs,
		m.environment,
		m.inbound,
	)
	if err != nil {
		return nil, err
	}
	m.childrenMu.Lock()
	m.children[account.ID] = child
	m.childrenMu.Unlock()

	m.initializationMu.RLock()
	params := append(json.RawMessage(nil), m.initializeParams...)
	initialized := m.initialized
	m.initializationMu.RUnlock()
	if len(params) > 0 {
		requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		_, err := child.Request(requestCtx, "initialize", params)
		cancel()
		if err != nil {
			return nil, err
		}
		if initialized {
			_ = child.Send(protocol.Message{Method: "initialized"})
		}
	}
	return child, nil
}

func (m *Multiplexer) SubscribeEvents() (<-chan Event, func()) {
	channel := make(chan Event, 32)
	m.eventsMu.Lock()
	m.events[channel] = struct{}{}
	m.eventsMu.Unlock()
	return channel, func() {
		m.eventsMu.Lock()
		if _, ok := m.events[channel]; ok {
			delete(m.events, channel)
			close(channel)
		}
		m.eventsMu.Unlock()
	}
}

func (m *Multiplexer) publish(event Event) {
	m.eventsMu.RLock()
	defer m.eventsMu.RUnlock()
	for channel := range m.events {
		select {
		case channel <- event:
		default:
		}
	}
}

func (m *Multiplexer) publishAccountRefresh(accountID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snapshot, err := m.accountSnapshot(ctx, accountID)
	if err == nil {
		m.publish(Event{Type: "account-updated", AccountID: accountID, Data: snapshot})
	}
}

func threadIDFromParams(params json.RawMessage) string {
	if len(params) == 0 {
		return ""
	}
	var decoded map[string]any
	if json.Unmarshal(params, &decoded) != nil {
		return ""
	}
	for _, key := range []string{"threadId", "thread_id"} {
		if value, ok := decoded[key].(string); ok {
			return value
		}
	}
	return ""
}

func threadIDFromResult(result json.RawMessage) string {
	var decoded struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(result, &decoded) != nil {
		return ""
	}
	return decoded.Thread.ID
}

func threadIDFromNotification(params json.RawMessage) string {
	if id := threadIDFromParams(params); id != "" {
		return id
	}
	return threadIDFromResult(params)
}

func accountHasCapacity(snapshot AccountSnapshot) bool {
	if !snapshot.Enabled || !snapshot.Connected || snapshot.AuthType != "chatgpt" {
		return false
	}
	weekly, short := longestAndShortestWindow(snapshot.RateLimits)
	return (weekly == nil || weekly.UsedPercent < 100) && (short == nil || short.UsedPercent < 100)
}

func isUsageLimitResponse(message protocol.Message) bool {
	if message.Error == nil {
		return false
	}
	text := strings.ToLower(message.Error.Message + " " + string(message.Error.Data))
	return strings.Contains(text, "usage_limit") ||
		strings.Contains(text, "usage limit") ||
		strings.Contains(text, "rate_limit") ||
		strings.Contains(text, "rate limit") ||
		strings.Contains(text, "quota")
}

func (m *Multiplexer) allSubscriptionsDepleted(ctx context.Context, id json.RawMessage) protocol.Message {
	var resetsAt *int64
	if preview := m.currentRateLimitPreview(); preview != nil && preview.Mode.isAllDepleted() {
		resetsAt = preview.ResetsAt
	} else if limits, err := m.AggregatedRateLimits(ctx); err == nil {
		weekly, _ := longestAndShortestWindow(limits)
		if weekly != nil {
			resetsAt = weekly.ResetsAt
		}
	}
	return allSubscriptionsDepleted(id, resetsAt)
}

func allSubscriptionsDepleted(id json.RawMessage, resetsAt *int64) protocol.Message {
	message := "All connected subscriptions are depleted. Add another subscription or wait for usage to reset."
	if resetsAt != nil {
		reset := time.Unix(*resetsAt, 0).In(time.Local)
		message = fmt.Sprintf(
			"All connected subscriptions are depleted. Usage resets on %s.",
			reset.Format("Monday, 2 January at 3:04 PM"),
		)
	}
	return protocol.Failure(
		id,
		-32026,
		message,
	)
}

func cloneAccountSet(source map[string]struct{}) map[string]struct{} {
	if len(source) == 0 {
		return nil
	}
	clone := make(map[string]struct{}, len(source))
	for accountID := range source {
		clone[accountID] = struct{}{}
	}
	return clone
}

func sortThreads(threads []map[string]any) {
	sort.SliceStable(threads, func(i, j int) bool {
		return numericField(threads[i], "updatedAt", "createdAt") > numericField(threads[j], "updatedAt", "createdAt")
	})
}

func numericField(value map[string]any, keys ...string) float64 {
	for _, key := range keys {
		if number, ok := value[key].(float64); ok {
			return number
		}
	}
	return 0
}
