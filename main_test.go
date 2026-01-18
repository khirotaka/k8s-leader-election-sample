package main

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// TestLeaderElection_SingleCandidate はシングル候補者がリーダーに選出されることをテストします
func TestLeaderElection_SingleCandidate(t *testing.T) {
	// Arrange: Fake clientset を作成
	clientset := fake.NewSimpleClientset()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      "test-lease",
			Namespace: "default",
		},
		Client: clientset.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: "test-pod-1",
		},
	}

	// Act: リーダー選出を実行
	leaderElected := make(chan struct{})
	var wg sync.WaitGroup

	wg.Go(func() {
		leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
			Lock:            lock,
			ReleaseOnCancel: true,
			LeaseDuration:   5 * time.Second,
			RenewDeadline:   3 * time.Second,
			RetryPeriod:     1 * time.Second,
			Callbacks: leaderelection.LeaderCallbacks{
				OnStartedLeading: func(ctx context.Context) {
					close(leaderElected)
					// リーダーとしての処理をシミュレート
					<-ctx.Done()
				},
				OnStoppedLeading: func() {
					t.Log("Leadership lost")
				},
				OnNewLeader: func(identity string) {
					t.Logf("New leader: %s", identity)
				},
			},
		})
	})

	// Assert: リーダーになることを確認
	select {
	case <-leaderElected:
		t.Log("✅ Successfully became leader")
	case <-time.After(10 * time.Second):
		t.Fatal("❌ Timeout waiting to become leader")
	}

	// クリーンアップ
	cancel()
	wg.Wait()
}

// TestLeaderElection_MultipleCandidates は複数候補者の中から1つだけがリーダーに選出されることをテストします
func TestLeaderElection_MultipleCandidates(t *testing.T) {
	clientset := fake.NewSimpleClientset()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const numCandidates = 3
	leaders := make(chan string, numCandidates)
	var wg sync.WaitGroup

	// 複数の候補者を起動
	for i := range numCandidates {
		wg.Add(1)
		podName := fmt.Sprintf("test-pod-%d", i)

		go func(identity string) {
			defer wg.Done()

			lock := &resourcelock.LeaseLock{
				LeaseMeta: metav1.ObjectMeta{
					Name:      "test-lease-multi",
					Namespace: "default",
				},
				Client: clientset.CoordinationV1(),
				LockConfig: resourcelock.ResourceLockConfig{
					Identity: identity,
				},
			}

			leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
				Lock:            lock,
				ReleaseOnCancel: true,
				LeaseDuration:   5 * time.Second,
				RenewDeadline:   3 * time.Second,
				RetryPeriod:     1 * time.Second,
				Callbacks: leaderelection.LeaderCallbacks{
					OnStartedLeading: func(ctx context.Context) {
						leaders <- identity
						<-ctx.Done()
					},
					OnStoppedLeading: func() {},
					OnNewLeader:      func(identity string) {},
				},
			})
		}(podName)
	}

	// Assert: 1つだけがリーダーになることを確認
	select {
	case leader := <-leaders:
		t.Logf("✅ Leader elected: %s", leader)

		// 短時間待機して他のリーダーがいないことを確認
		select {
		case duplicateLeader := <-leaders:
			t.Fatalf("❌ Multiple leaders detected: %s", duplicateLeader)
		case <-time.After(3 * time.Second):
			t.Log("✅ Only one leader exists")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("❌ No leader elected")
	}

	cancel()
	wg.Wait()
}

// TestLeaderElection_GracefulShutdown はリーダーシップの正常終了をテストします
func TestLeaderElection_GracefulShutdown(t *testing.T) {
	clientset := fake.NewSimpleClientset()

	ctx, cancel := context.WithCancel(context.Background())

	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      "test-lease-shutdown",
			Namespace: "default",
		},
		Client: clientset.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: "test-pod-1",
		},
	}

	leadershipLost := make(chan struct{})
	leaderStarted := make(chan struct{})

	go func() {
		leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
			Lock:            lock,
			ReleaseOnCancel: true, // 重要: キャンセル時にリーダーシップを放棄
			LeaseDuration:   5 * time.Second,
			RenewDeadline:   3 * time.Second,
			RetryPeriod:     1 * time.Second,
			Callbacks: leaderelection.LeaderCallbacks{
				OnStartedLeading: func(ctx context.Context) {
					close(leaderStarted)
					<-ctx.Done()
				},
				OnStoppedLeading: func() {
					close(leadershipLost)
				},
				OnNewLeader: func(identity string) {},
			},
		})
	}()

	// リーダーになるのを待つ
	select {
	case <-leaderStarted:
		t.Log("Leader started")
	case <-time.After(10 * time.Second):
		t.Fatal("❌ Timeout waiting for leader to start")
	}

	// コンテキストをキャンセル
	cancel()

	// リーダーシップが放棄されることを確認
	select {
	case <-leadershipLost:
		t.Log("✅ Leadership gracefully released")
	case <-time.After(10 * time.Second):
		t.Fatal("❌ Leadership was not released")
	}
}

// TestLeaderElection_LeaderCallbacks はコールバック関数が正しく呼び出されることをテストします
func TestLeaderElection_LeaderCallbacks(t *testing.T) {
	clientset := fake.NewSimpleClientset()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      "test-lease-callbacks",
			Namespace: "default",
		},
		Client: clientset.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: "test-pod-1",
		},
	}

	// コールバック呼び出しを追跡するためのチャンネル
	onStartedLeadingCalled := make(chan struct{})
	onNewLeaderCalled := make(chan string, 1)

	var wg sync.WaitGroup

	wg.Go(func() {
		leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
			Lock:            lock,
			ReleaseOnCancel: true,
			LeaseDuration:   5 * time.Second,
			RenewDeadline:   3 * time.Second,
			RetryPeriod:     1 * time.Second,
			Callbacks: leaderelection.LeaderCallbacks{
				OnStartedLeading: func(ctx context.Context) {
					close(onStartedLeadingCalled)
					<-ctx.Done()
				},
				OnStoppedLeading: func() {},
				OnNewLeader: func(identity string) {
					select {
					case onNewLeaderCalled <- identity:
					default:
					}
				},
			},
		})
	})

	// OnNewLeader コールバックが呼び出されることを確認
	select {
	case identity := <-onNewLeaderCalled:
		t.Logf("✅ OnNewLeader called with identity: %s", identity)
		if identity != "test-pod-1" {
			t.Fatalf("❌ Expected identity 'test-pod-1', got '%s'", identity)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("❌ OnNewLeader callback was not called")
	}

	// OnStartedLeading コールバックが呼び出されることを確認
	select {
	case <-onStartedLeadingCalled:
		t.Log("✅ OnStartedLeading callback called")
	case <-time.After(10 * time.Second):
		t.Fatal("❌ OnStartedLeading callback was not called")
	}

	cancel()
	wg.Wait()
}

// TestLeaderElection_LeaseExists はLeaseリソースが作成されることをテストします
func TestLeaderElection_LeaseExists(t *testing.T) {
	clientset := fake.NewSimpleClientset()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	leaseName := "test-lease-exists"
	leaseNamespace := "default"

	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      leaseName,
			Namespace: leaseNamespace,
		},
		Client: clientset.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: "test-pod-1",
		},
	}

	leaderElected := make(chan struct{})
	var wg sync.WaitGroup

	wg.Go(func() {
		leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
			Lock:            lock,
			ReleaseOnCancel: true,
			LeaseDuration:   5 * time.Second,
			RenewDeadline:   3 * time.Second,
			RetryPeriod:     1 * time.Second,
			Callbacks: leaderelection.LeaderCallbacks{
				OnStartedLeading: func(ctx context.Context) {
					close(leaderElected)
					<-ctx.Done()
				},
				OnStoppedLeading: func() {},
				OnNewLeader:      func(identity string) {},
			},
		})
	})

	// リーダーになるまで待つ
	select {
	case <-leaderElected:
		t.Log("Leader elected, checking Lease")
	case <-time.After(10 * time.Second):
		t.Fatal("❌ Timeout waiting to become leader")
	}

	// Leaseリソースが存在することを確認
	lease, err := clientset.CoordinationV1().Leases(leaseNamespace).Get(ctx, leaseName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("❌ Failed to get lease: %v", err)
	}

	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != "test-pod-1" {
		t.Fatalf("❌ Expected holder identity 'test-pod-1', got '%v'", lease.Spec.HolderIdentity)
	}

	t.Logf("✅ Lease exists with holder: %s", *lease.Spec.HolderIdentity)

	cancel()
	wg.Wait()
}

// TestLeaderElection_TimingParameters はリーダー選出のタイミングパラメータをテストします
func TestLeaderElection_TimingParameters(t *testing.T) {
	clientset := fake.NewSimpleClientset()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	startTime := time.Now()
	leaderElected := make(chan struct{})

	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      "test-lease-timing",
			Namespace: "default",
		},
		Client: clientset.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: "test-pod-1",
		},
	}

	var wg sync.WaitGroup

	wg.Go(func() {
		leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
			Lock:            lock,
			ReleaseOnCancel: true,
			LeaseDuration:   5 * time.Second,
			RenewDeadline:   3 * time.Second,
			RetryPeriod:     1 * time.Second,
			Callbacks: leaderelection.LeaderCallbacks{
				OnStartedLeading: func(ctx context.Context) {
					close(leaderElected)
					<-ctx.Done()
				},
				OnStoppedLeading: func() {},
				OnNewLeader:      func(identity string) {},
			},
		})
	})

	// リーダーになるまで待つ
	select {
	case <-leaderElected:
		electionTime := time.Since(startTime)
		t.Logf("✅ Leader elected in %v", electionTime)

		// リーダー選出は RetryPeriod 以内に完了するはず（初回の候補者の場合）
		if electionTime > 5*time.Second {
			t.Logf("⚠️ Leader election took longer than expected: %v", electionTime)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("❌ Timeout waiting to become leader")
	}

	cancel()
	wg.Wait()
}
