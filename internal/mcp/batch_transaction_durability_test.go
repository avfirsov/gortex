package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func durabilityEventIndex(events []string, prefix string, after int) int {
	for i := after + 1; i < len(events); i++ {
		if strings.HasPrefix(events[i], prefix) {
			return i
		}
	}
	return -1
}

func TestAtomicBatchDurabilityOrderAndDirectoryDeduplication(t *testing.T) {
	var scheduled atomic.Int64
	s := newAtomicBatchTestServer(t, mutationTestWatcher{scheduled: &scheduled})
	targetDir := t.TempDir()
	paths := []string{
		writeAtomicBatchFixture(t, targetDir, "a.txt", "a0\n"),
		writeAtomicBatchFixture(t, targetDir, "b.txt", "b0\n"),
	}
	transactionID := "durability-order"
	transactionDir := batchTransactionDir(transactionID)
	transactionRoot := filepath.Dir(transactionDir)
	var events []string
	s.batchDurabilityOverride = &batchDurabilityOps{
		writeFile: func(path string, content []byte, mode os.FileMode) error {
			base := filepath.Base(path)
			switch {
			case base == "manifest.json":
				var receipt batchTransactionReceipt
				if err := json.Unmarshal(content, &receipt); err != nil {
					t.Fatalf("decode manifest event: %v", err)
				}
				events = append(events, fmt.Sprintf("manifest:%s:%s", receipt.Status, receipt.GraphStatus))
			case strings.HasPrefix(base, "before-"):
				events = append(events, "backup:"+base)
			default:
				events = append(events, "target:"+base)
			}
			return durableAtomicWriteFile(path, content, mode)
		},
		syncDirectory: func(path string) error {
			switch filepath.Clean(path) {
			case filepath.Clean(targetDir):
				events = append(events, "sync:target")
			case filepath.Clean(transactionDir):
				events = append(events, "sync:journal")
			case filepath.Clean(transactionRoot):
				events = append(events, "sync:journal-root")
			default:
				events = append(events, "sync:"+filepath.Clean(path))
			}
			return syncBatchDirectory(path)
		},
		removeFile: func(path string) error {
			events = append(events, "remove:"+filepath.Base(path))
			return os.Remove(path)
		},
	}

	receipt, err := s.runBatchTransaction(context.Background(), []batchEditItem{
		atomicFileEdit(paths[0], "a0", "a1"),
		atomicFileEdit(paths[1], "b0", "b1"),
	}, transactionID)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "committed" || receipt.DiskStatus != "committed" || receipt.GraphStatus != "fresh" {
		t.Fatalf("receipt = %+v", receipt)
	}

	backupA := durabilityEventIndex(events, "backup:before-0000.bin", -1)
	backupB := durabilityEventIndex(events, "backup:before-0001.bin", backupA)
	prepared := durabilityEventIndex(events, "manifest:prepared:", backupB)
	preparedSync := durabilityEventIndex(events, "sync:journal", prepared)
	firstTarget := durabilityEventIndex(events, "target:", preparedSync)
	secondTarget := durabilityEventIndex(events, "target:", firstTarget)
	targetSync := durabilityEventIndex(events, "sync:target", secondTarget)
	committed := durabilityEventIndex(events, "manifest:committed:pending", targetSync)
	finalManifest := durabilityEventIndex(events, "manifest:committed:fresh", committed)
	finalManifestSync := durabilityEventIndex(events, "sync:journal", finalManifest)
	firstRemove := durabilityEventIndex(events, "remove:", finalManifestSync)
	if backupA < 0 || backupB < 0 || prepared < 0 || preparedSync < 0 || firstTarget < 0 || secondTarget < 0 || targetSync < 0 || committed < 0 || finalManifest < 0 || finalManifestSync < 0 || firstRemove < 0 {
		t.Fatalf("durability events are out of order: %v", events)
	}
	var targetSyncs int
	for _, event := range events {
		if event == "sync:target" {
			targetSyncs++
		}
	}
	if targetSyncs != 1 {
		t.Fatalf("target directory synced %d times for two same-directory edits; events=%v", targetSyncs, events)
	}
}

func TestAtomicBatchPreparedJournalSyncFailureWritesNoTargets(t *testing.T) {
	s := newAtomicBatchTestServer(t, mutationTestWatcher{})
	path := writeAtomicBatchFixture(t, t.TempDir(), "target.txt", "old\n")
	transactionID := "prepared-sync-failure"
	transactionDir := filepath.Clean(batchTransactionDir(transactionID))
	var targetWrites atomic.Int64
	s.batchWriteOverride = func(string, []byte, os.FileMode) error {
		targetWrites.Add(1)
		return nil
	}
	s.batchDurabilityOverride = &batchDurabilityOps{
		syncDirectory: func(path string) error {
			if filepath.Clean(path) == transactionDir {
				return errors.New("injected journal fsync failure")
			}
			return syncBatchDirectory(path)
		},
	}

	receipt, err := s.runBatchTransaction(context.Background(), []batchEditItem{
		atomicFileEdit(path, "old", "new"),
	}, transactionID)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "aborted" || receipt.DiskStatus != "unchanged" || targetWrites.Load() != 0 {
		t.Fatalf("receipt=%+v target_writes=%d", receipt, targetWrites.Load())
	}
	if got := readAtomicBatchFixture(t, path); got != "old\n" {
		t.Fatalf("target changed before prepared journal became durable: %q", got)
	}
	if _, err := os.Stat(filepath.Join(transactionDir, "before-0000.bin")); err != nil {
		t.Fatalf("failed prepared journal discarded recovery backup: %v", err)
	}
}

func TestAtomicBatchTargetDirectorySyncFailureRollsBackDurably(t *testing.T) {
	s := newAtomicBatchTestServer(t, mutationTestWatcher{})
	targetDir := t.TempDir()
	paths := []string{
		writeAtomicBatchFixture(t, targetDir, "a.txt", "a0\n"),
		writeAtomicBatchFixture(t, targetDir, "b.txt", "b0\n"),
	}
	var targetSyncCalls atomic.Int64
	s.batchDurabilityOverride = &batchDurabilityOps{
		syncDirectory: func(path string) error {
			if filepath.Clean(path) == filepath.Clean(targetDir) && targetSyncCalls.Add(1) == 1 {
				return errors.New("injected target directory fsync failure")
			}
			return syncBatchDirectory(path)
		},
	}

	receipt, err := s.runBatchTransaction(context.Background(), []batchEditItem{
		atomicFileEdit(paths[0], "a0", "a1"),
		atomicFileEdit(paths[1], "b0", "b1"),
	}, "target-sync-failure")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "aborted" || receipt.DiskStatus != "rolled_back" {
		t.Fatalf("receipt = %+v", receipt)
	}
	if targetSyncCalls.Load() != 2 {
		t.Fatalf("target directory sync calls = %d, want failed commit sync plus durable rollback sync", targetSyncCalls.Load())
	}
	if readAtomicBatchFixture(t, paths[0]) != "a0\n" || readAtomicBatchFixture(t, paths[1]) != "b0\n" {
		t.Fatal("directory fsync failure did not restore original files")
	}
}

func TestAtomicBatchTerminalManifestFailureRetainsRecoveryBackups(t *testing.T) {
	s := newAtomicBatchTestServer(t, mutationTestWatcher{})
	path := writeAtomicBatchFixture(t, t.TempDir(), "target.txt", "old\n")
	transactionID := "terminal-manifest-failure"
	s.batchDurabilityOverride = &batchDurabilityOps{
		writeFile: func(path string, content []byte, mode os.FileMode) error {
			if filepath.Base(path) == "manifest.json" {
				var receipt batchTransactionReceipt
				if err := json.Unmarshal(content, &receipt); err != nil {
					return err
				}
				if receipt.Status == "committed" {
					return errors.New("injected terminal manifest failure")
				}
			}
			return durableAtomicWriteFile(path, content, mode)
		},
	}

	receipt, err := s.runBatchTransaction(context.Background(), []batchEditItem{
		atomicFileEdit(path, "old", "new"),
	}, transactionID)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "committed" || receipt.DiskStatus != "committed" || !strings.Contains(receipt.Error, "terminal journal update failed") {
		t.Fatalf("receipt = %+v", receipt)
	}
	if got := readAtomicBatchFixture(t, path); got != "new\n" {
		t.Fatalf("committed content = %q", got)
	}
	backupPath := filepath.Join(batchTransactionDir(transactionID), "before-0000.bin")
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("terminal manifest failure discarded recovery backup: %v", err)
	}
	persisted, found, err := readBatchManifest(transactionID)
	if err != nil || !found || persisted.Status != "prepared" {
		t.Fatalf("persisted recovery point = %+v, found=%v, err=%v", persisted, found, err)
	}
}

func TestAtomicBatchRecoveryConflictRetainsBackup(t *testing.T) {
	s := newAtomicBatchTestServer(t, mutationTestWatcher{})
	path := writeAtomicBatchFixture(t, t.TempDir(), "target.txt", "old\n")
	transactionID := "recovery-conflict-backup"
	prepareAtomicRecoveryFixture(t, s, transactionID, []string{path}, []string{"old\n"}, []string{"new\n"})
	if err := durableAtomicWriteFile(path, []byte("external\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	restarted := &Server{watcher: mutationTestWatcher{}, session: newSessionState()}
	receipt, err := restarted.batchTransactionStatus(context.Background(), transactionID)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "recovery_conflict" || receipt.DiskStatus != "conflict" {
		t.Fatalf("receipt = %+v", receipt)
	}
	backupPath := filepath.Join(batchTransactionDir(transactionID), "before-0000.bin")
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("recovery conflict discarded backup needed for operator repair: %v", err)
	}
	if got := readAtomicBatchFixture(t, path); got != "external\n" {
		t.Fatalf("recovery conflict overwrote external content: %q", got)
	}
}

func TestBatchTransactionJournalPathConfinesCallerID(t *testing.T) {
	root := filepath.Join(t.TempDir(), "transactions")
	t.Setenv(batchTransactionDirEnv, root)
	dir := batchTransactionDir("../../outside/transaction")
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || strings.Contains(filepath.Base(dir), string(filepath.Separator)) {
		t.Fatalf("transaction ID escaped journal root: root=%q dir=%q rel=%q", root, dir, rel)
	}
}
