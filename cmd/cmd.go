package cmd

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/golang/glog"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Azure/kubernetes-watchdog/pkg/azure"
	"github.com/Azure/kubernetes-watchdog/pkg/watchdog"
)

var (
	shutdownSignals      = []os.Signal{os.Interrupt, syscall.SIGTERM}
	onlyOneSignalHandler = make(chan struct{})

	cfg                  watchdog.WatchDogConfig
	cloudProviderName    string
	cloudProviderInitStr string

	rootCmd = &cobra.Command{
		Use:   "kubernetes-disk-watchdog",
		Short: "Runs kubernetes-disk-watchdog",
		Long:  ``,
	}

	runCmd = &cobra.Command{
		Use:   "run",
		Short: "runs the watch dog",
		Long: `The watch dog watches for nodes and pvcs
										- if node is is not NotReady it will be added to watch list
										- Watch list logic
										-- Phase 0 being in watch list
										-- phase 1 being in watch list for n sec
										-- phase 2 + Powered down
										-- phase 3: nodes Phased 2 and aged for n sec
										example:
										# kubernetes-disk-watchdog using kubeconfig 
										kubernetes-disk-watchdog run --kubeconfig <path> --service-principal-client-id "" --service-principal-password ""
										`,
		Run: func(cmd *cobra.Command, args []string) {
			//TODO: logic to switch clouds
			stop := setupSignalHandler()

			cloudHandler, err := azure.NewAzureCloudHandler(cloudProviderInitStr)
			if nil != err {
				glog.Errorf("error creating cloud handler: %v", err.Error())
				os.Exit(1)
			}

			wd, err := watchdog.NewWatchdog(cfg, cloudHandler)
			if nil != err {
				glog.Errorf("error creating watchdog: %v", err.Error())
				os.Exit(1)
			}

			if err := wd.Run(stop); nil != err {
				glog.Errorf("Error running the watchdog: %v", err.Error())
				os.Exit(1)
			}

		},
	}
)

func validate() {
	if "azure" != cloudProviderName {
		glog.Errorf("cloud providers other than azure are not supported yet")
		os.Exit(1)
	}
}
func setupSignalHandler() (stopCh <-chan struct{}) {
	close(onlyOneSignalHandler) // panics when called twice

	stop := make(chan struct{})
	c := make(chan os.Signal, 2)
	signal.Notify(c, shutdownSignals...)
	go func() {
		<-c
		glog.Infof("received stop signal, stopping")
		close(stop)
		<-c
		os.Exit(1) // second signal. Exit directly.
	}()

	return stop
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		glog.Fatalf("error running: %v", err.Error())
		os.Exit(1)
	}
}

func init() {
	// glow to pflag/flag
	pflag.Set("logtostderr", "true")
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)

	runCmd.PersistentFlags().StringVarP(&cfg.KubeConfigPath, "kubeconfig", "k", "", "kubeconfig path. if empty in cluster config will be used")
	runCmd.PersistentFlags().DurationVarP(&cfg.SyncInterval, "sync-interval", "r", time.Second*30, "controller cache sync")
	runCmd.PersistentFlags().DurationVarP(&cfg.RunInterval, "run-interval", "s", time.Second*10, "time between run. each run checks tracked node status check)")
	runCmd.PersistentFlags().DurationVarP(&cfg.StageOneDuration, "stageone-wait", "a", time.Second*30, "time for a not 'Ready' node to go into checking power status stage)")
	runCmd.PersistentFlags().DurationVarP(&cfg.StageTwoDuration, "stagetwo-wait", "b", time.Second*30, "time waiting on a not 'Ready' node before checking power status)")
	runCmd.PersistentFlags().DurationVarP(&cfg.StageThreeDuration, "stagethree-wait", "c", time.Second*30, "time waiting on a powered down not 'Ready' node before detaching disks)")

	runCmd.PersistentFlags().BoolVarP(&cfg.ForceRestartStageTwo, "force-stagetwo-restart", "", false, "force a restart for  powered on not 'Ready' node)")
	runCmd.PersistentFlags().DurationVarP(&cfg.StageTwoRestartDuration, "force-stagetwo-restart-wait", "f", time.Minute*5, "time waiting on a powered on not 'Ready' node before forcing restart)")

	runCmd.PersistentFlags().DurationVarP(&cfg.MaxNodeProcessingTime, "max-node-processing-time", "m", time.Second*120, "max allocated time for processing a single node (wraps all arm calls))")

	runCmd.PersistentFlags().BoolVarP(&cfg.TrackMasters, "track-masters", "", false, "if enabled masters will be tracked for failures. (use it if masters has no data disks)")

	runCmd.PersistentFlags().StringVarP(&cloudProviderName, "cloud-provider", "p", "azure", "cloud provider to use")
	runCmd.PersistentFlags().StringVarP(&cloudProviderInitStr, "cloud-provider-init", "i", "", "cloud provider free form init string")

	// Leader Election Flags
	runCmd.PersistentFlags().StringVarP(&cfg.LeaderElectionNamespace, "leader-election-namespace", "n", "default", "namespace to create leader election object, 'default' namespace is default")
	runCmd.PersistentFlags().DurationVarP(&cfg.LeaderElectionTtl, "leader-election-ttl", "", time.Second*30, "leader election ttl")
	runCmd.PersistentFlags().StringVarP(&cfg.LeaderElectionId, "leader-election-id", "", "kbuernetes-watchdog", "leader election id (should be unique within cluster)")

	hostName, err := os.Hostname()
	if nil != err {
		glog.Fatalf("Failed to get current hostname")
		os.Exit(1)
	}
	hostName = fmt.Sprintf("%s-%d", hostName, os.Getpid())

	runCmd.PersistentFlags().StringVarP(&cfg.ThisLeaderName, "leader-election-name", "", hostName, "current leader name, 'hostname' is default")

	rootCmd.AddCommand(runCmd)
}
