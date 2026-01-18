package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

func main() {
	// Pod名を環境変数から取得
	podName := os.Getenv("POD_NAME")
	if podName == "" {
		log.Fatal("POD_NAME environment variable must be set")
	}

	namespace := os.Getenv("NAMESPACE")
	if namespace == "" {
		namespace = "leader-election-demo"
	}

	// Kubernetes クライアントの作成
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("Failed to get in-cluster config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("Failed to create clientset: %v", err)
	}

	// リーダー選出の設定
	leaseLockName := "leader-election-lease"
	leaseLockNamespace := namespace

	// リソースロックの作成
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      leaseLockName,
			Namespace: leaseLockNamespace,
		},
		Client: clientset.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: podName,
		},
	}

	// シグナルハンドリング用のコンテキスト
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// SIGTERMとSIGINTをキャッチ
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-signalChan
		log.Printf("[%s] Received termination signal, shutting down...", podName)
		cancel()
	}()

	// 現在のリーダーを追跡
	var currentLeader string

	// リーダー状態を監視するゴルーチン
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if currentLeader == podName {
					log.Printf("[%s] >>> I am now the LEADER <<<", podName)
				} else if currentLeader != "" {
					log.Printf("[%s] New leader elected: %s", podName, currentLeader)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// リーダー選出の実行
	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   15 * time.Second,
		RenewDeadline:   10 * time.Second,
		RetryPeriod:     2 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				currentLeader = podName
				log.Printf("[%s] >>> I am now the LEADER <<<", podName)
			},
			OnStoppedLeading: func() {
				log.Printf("[%s] Lost leadership", podName)
			},
			OnNewLeader: func(identity string) {
				if identity == podName {
					return
				}
				currentLeader = identity
				log.Printf("[%s] New leader elected: %s", podName, identity)
			},
		},
	})

	log.Printf("[%s] Leader election stopped", podName)
}
