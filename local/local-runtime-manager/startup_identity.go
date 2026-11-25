package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

// The locator is inherited by the live process-compose supervisor. It confers
// no authority: both ends identify their peer via the OS before exchanging data.
const startupIdentityEnv = "LAZYMIND_STARTUP_IDENTITY_PEER"
const nativeIdentityTimeout = 75 * time.Second

type startupIdentityLocator struct {
	Address string
	Manager desktopupdate.ProcessIdentity
}

func startupIdentityBinding(record *desktopupdate.NativeRecord) [sha256.Size]byte {
	release, version, _, _ := record.EffectiveTarget()
	raw, _ := json.Marshal([]string{record.TransactionID, record.Owner, release, version,
		record.InstallationRoot, record.RuntimeRoot, record.TrustDomain, record.Audience,
		record.Channel, record.SigningIdentity, record.Platform, record.Arch, record.PolicyDigest})
	return sha256.Sum256(raw)
}

func processIsManager(actor desktopupdate.ProcessIdentity, image os.FileInfo) bool {
	path, err := identityPeerExecutable(actor)
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || !os.SameFile(info, image) {
		return false
	}
	alive, err := desktopupdate.SameProcess(actor)
	return err == nil && alive
}

func installedManagerImage(paths RuntimePaths) (os.FileInfo, error) {
	file, err := os.Open(filepath.Join(paths.ResourcesRoot, "bin", executableName("local-runtime-manager")))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return file.Stat()
}

type startupIdentityServer struct {
	ctx           context.Context
	cancel        context.CancelFunc
	listener      net.Listener
	closeListener sync.Once
	image         os.FileInfo
	paths         RuntimePaths
	locator       string
	binding       [sha256.Size]byte
	supervisor    desktopupdate.ProcessIdentity
	mu            sync.Mutex
	current       *startupVerification
	retired       bool
}

type startupIdentityReply struct {
	Status string                `json:"status"`
	Config *desktopupdate.Config `json:"config,omitempty"`
}

type startupVerification struct {
	ctx       context.Context
	done      chan struct{}
	drained   chan struct{}
	cancel    context.CancelFunc
	waiters   int
	reply     startupIdentityReply
	unchanged func() bool
}

func newStartupIdentityServer(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) *startupIdentityServer {
	bootstrap := cfg.UpdateBootstrap
	if cfg.Profile != "desktop" || bootstrap == nil || cfg.MaintenanceMode != "" {
		return nil
	}
	root, err := resolveRuntimeRecoveryRoot()
	if err != nil {
		return nil
	}
	record, err := desktopupdate.ReadNativeRecord(root)
	if err != nil || record == nil || record.Phase != "activated" || record.TransactionID != bootstrap.TransactionID ||
		record.Owner == "" || record.Owner != cfg.OwnerToken || bootstrap.Owner != cfg.OwnerToken ||
		!sameRuntimePath(record.InstallationRoot, paths.ResourcesRoot) || !sameRuntimePath(record.RuntimeRoot, paths.RuntimeRoot) {
		return nil
	}
	release, _, _, _ := record.EffectiveTarget()
	if release != bootstrap.ReleaseID {
		return nil
	}
	actor, err := desktopupdate.CurrentProcessIdentity()
	if err != nil {
		return nil
	}
	image, err := installedManagerImage(paths)
	if err != nil || !processIsManager(actor, image) {
		return nil
	}
	listener, address, err := listenIdentityPeer()
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &startupIdentityServer{ctx: ctx, cancel: cancel, listener: listener, image: image, paths: paths, binding: startupIdentityBinding(record)}
	context.AfterFunc(ctx, s.closeEndpoint)
	hint, _ := json.Marshal(startupIdentityLocator{Address: address, Manager: actor})
	s.locator = string(hint)
	return s
}

// At readiness, future service starts verify independently. Requests already
// admitted to the batch retain their bounded verification until they finish.
func (s *startupIdentityServer) stopAccepting() {
	s.mu.Lock()
	s.retired = true
	s.mu.Unlock()
	s.closeEndpoint()
}

func (s *startupIdentityServer) closeEndpoint() {
	s.closeListener.Do(func() { _ = s.listener.Close() })
}

// Start accepting after the manager itself has launched and identified its
// supervisor. No PID file or PID supplied by a client participates in this decision.
func (s *startupIdentityServer) serve(pid int) {
	supervisor, err := desktopupdate.IdentifyProcess(pid)
	if err != nil {
		s.cancel()
		return
	}
	s.supervisor = supervisor
	go func() {
		defer s.closeEndpoint()
		slots := make(chan struct{}, 64)
		for {
			conn, err := s.listener.Accept()
			if err != nil {
				return
			}
			select {
			case slots <- struct{}{}:
				go func() {
					defer func() { <-slots }()
					s.handle(conn)
				}()
			default:
				_ = conn.Close()
			}
		}
	}()
}

func (s *startupIdentityServer) handle(conn net.Conn) {
	defer conn.Close()
	stop := context.AfterFunc(s.ctx, func() { _ = conn.Close() })
	defer stop()
	_ = conn.SetDeadline(time.Now().Add(nativeIdentityTimeout))
	actor, err := identityPeerProcess(conn)
	if err != nil || !identityPeerDescends(s.ctx, actor, s.supervisor) || !processIsManager(actor, s.image) {
		return
	}
	var binding [sha256.Size]byte
	if _, err := io.ReadFull(conn, binding[:]); err != nil || binding != s.binding {
		return
	}
	flight := s.join()
	if flight == nil {
		// This connection was accepted before the normal readiness cutoff,
		// but never joined an in-flight verification.
		if s.ctx.Err() == nil {
			_ = json.NewEncoder(conn).Encode(startupIdentityReply{Status: "verify-independently"})
		}
		return
	}
	defer s.leave(flight)
	disconnected := make(chan struct{})
	go func() {
		var extra [1]byte
		_, _ = conn.Read(extra[:])
		close(disconnected)
	}()
	select {
	case <-disconnected:
		return
	case <-s.ctx.Done():
		return
	case <-flight.done:
		if flight.ctx.Err() != nil || flight.reply.Status == "" {
			return
		}
		if flight.unchanged != nil && !flight.unchanged() {
			return
		}
		_ = json.NewEncoder(conn).Encode(flight.reply)
	}
}

func (s *startupIdentityServer) join() *startupVerification {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.retired || s.ctx.Err() != nil {
		return nil
	}
	if s.current == nil {
		ctx, cancel := context.WithTimeout(s.ctx, nativeIdentityTimeout)
		s.current = &startupVerification{ctx: ctx, done: make(chan struct{}), drained: make(chan struct{}), cancel: cancel}
		go s.verify(ctx, s.current)
	}
	s.current.waiters++
	return s.current
}

func (s *startupIdentityServer) leave(flight *startupVerification) {
	s.mu.Lock()
	defer s.mu.Unlock()
	flight.waiters--
	if flight.waiters == 0 {
		if s.current == flight {
			s.current = nil
		}
		close(flight.drained)
		flight.cancel()
	}
}

func (s *startupIdentityServer) verify(ctx context.Context, flight *startupVerification) {
	unchanged, release, watchErr := watchStartupIdentityInputs(s.paths)
	if release != nil {
		defer release()
	}
	defer func() {
		close(flight.done)
		// Keep the observation and Windows file handles live through each
		// waiter's last check and response. A disconnected caller also leaves.
		<-flight.drained
	}()
	var before [sha256.Size]byte
	var verified desktopupdate.Config
	var err error
	verifyIndependently := watchErr != nil
	if !verifyIndependently {
		flight.unchanged = unchanged
		if unchanged() {
			before, err = snapshotStartupIdentityInputs(ctx, s.paths)
			if err == nil {
				verified, err = readNativeConfiguration(ctx, s.paths)
			} else if errors.Is(err, errStartupObservationUnavailable) {
				verifyIndependently = true
			}
		} else {
			err = updateStateFailure("native_install_unavailable")
		}
	}
	// Once the provider returns, new callers need another verification even
	// while this batch's final package check or response delivery is pending.
	s.mu.Lock()
	if s.current == flight {
		s.current = nil
	}
	s.mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	if verifyIndependently {
		// The filesystem could not supply an initial observation. No shared
		// verifier has run, so each caller can still verify independently.
		flight.reply = startupIdentityReply{Status: "verify-independently"}
		return
	}
	if err != nil || !unchanged() {
		return
	}
	after, err := snapshotStartupIdentityInputs(ctx, s.paths)
	if err == nil && ctx.Err() == nil && before == after && unchanged() {
		flight.reply = startupIdentityReply{Status: "verified", Config: &verified}
	}
}

// Only sealed internal service startup calls this function. A missing or
// unauthenticated endpoint leaves the normal independent verification available.
func readServiceStartupIdentity(ctx context.Context, paths RuntimePaths, record *desktopupdate.NativeRecord) (desktopupdate.Config, error) {
	bounded, cancel := context.WithTimeout(ctx, nativeIdentityTimeout)
	defer cancel()
	var hint startupIdentityLocator
	locator := os.Getenv(startupIdentityEnv)
	if len(locator) == 0 || len(locator) > 4096 || json.Unmarshal([]byte(locator), &hint) != nil || hint.Address == "" || hint.Manager.PID <= 0 || hint.Manager.StartID == 0 {
		return readNativeConfiguration(bounded, paths)
	}
	conn, err := dialIdentityPeer(bounded, hint.Address)
	if err != nil {
		return readNativeConfiguration(bounded, paths)
	}
	defer conn.Close()
	// Close on the caller's context itself. A separate socket deadline can
	// wake the read before that context has actually observed its deadline.
	stop := context.AfterFunc(bounded, func() { _ = conn.Close() })
	defer stop()
	server, peerErr := identityPeerProcess(conn)
	self, selfErr := desktopupdate.CurrentProcessIdentity()
	image, imageErr := installedManagerImage(paths)
	if peerErr != nil || server != hint.Manager || selfErr != nil || imageErr != nil ||
		!processIsManager(server, image) || !processIsManager(self, image) || !identityPeerDescends(bounded, self, server) {
		return readNativeConfiguration(bounded, paths)
	}
	binding := startupIdentityBinding(record)
	var reply startupIdentityReply
	if _, err := conn.Write(binding[:]); err != nil || json.NewDecoder(io.LimitReader(conn, 1<<20)).Decode(&reply) != nil || bounded.Err() != nil {
		return desktopupdate.Config{}, updateStateFailure("native_install_unavailable")
	}
	if reply.Status == "verify-independently" && reply.Config == nil {
		return readNativeConfiguration(bounded, paths)
	}
	if reply.Status != "verified" || reply.Config == nil || desktopupdate.ValidateConfig(*reply.Config) != nil {
		return desktopupdate.Config{}, updateStateFailure("native_install_unavailable")
	}
	// Both peers must still belong to the installation that made this request.
	current, err := installedManagerImage(paths)
	if err != nil || !os.SameFile(image, current) || !processIsManager(server, current) || !processIsManager(self, current) {
		return desktopupdate.Config{}, updateStateFailure("native_install_unavailable")
	}
	return *reply.Config, nil
}
