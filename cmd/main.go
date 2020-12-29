package main

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"io"
	v1 "k8s.io/api/core/v1"
	"path/filepath"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	// Load all auth plugins
	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

type options struct {
	labels string
	container string
	kubeConfig string
	namespace string
}

var (
	log  logr.Logger
	o = options{}

	rootCmd = &cobra.Command{
		Use:   "tailpods",
		Short: "Tail the logs of the most recent pod with the provided labels.",
		Run: func(cmd *cobra.Command, args []string) {
			setup()

			err := tail(o)

			if err != nil {
				log.Error(err, "run failed")
			}
		},
	}
)

// setup performs common app setup
func setup() {
	// Start with a production logger config.
	config := zap.NewProductionConfig()

	// TODO(jlewi): In development mode we should use the console encoder as opposed to json formatted logs.

	// Increment the logging level.
	// TODO(jlewi): Make this a flag.
	config.Level = zap.NewAtomicLevelAt(zap.DebugLevel)

	zapLog, err := config.Build()
	if err != nil {
		panic(fmt.Sprintf("Could not create zap instance (%v)?", err))
	}
	log = zapr.NewLogger(zapLog)

	// replace the global logger
	zap.ReplaceGlobals(zapLog)
}

func init() {
	// Get the default for the config file
	kubeconfig := ""
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = filepath.Join(home, ".kube", "config")
	}

	rootCmd.Flags().StringVarP(&o.labels, "labels", "l", "", "The label selector to match pods on")
	rootCmd.Flags().StringVarP(&o.namespace, "namespace", "n", "", "The namespace")
	rootCmd.Flags().StringVarP(&o.container, "container", "", "", "Container whose logs to tail")
	rootCmd.Flags().StringVarP(&o.kubeConfig, "kubeconfig", "", kubeconfig, "Absolute path to the kubeconfig file")
	rootCmd.MarkFlagRequired("labels")
	rootCmd.MarkFlagRequired("namespace")
}

func tail(o options) error {
	setup()


	// use the current context in kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", o.kubeConfig)
	if err != nil {
		return err
	}

	// create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}
	for {
		pods, err := clientset.CoreV1().Pods(o.namespace).List(context.TODO(), metav1.ListOptions{
			LabelSelector: o.labels,
		})
		if err != nil {
			panic(err.Error())
		}
		log.Info("Found matching pods", "num", len(pods.Items), "selector", o.labels)

		// Find the newest pod.
		latestPod := v1.Pod{}
		if len(pods.Items) > 0 {
			latestPod = pods.Items[0]
		}
		for _, p := range pods.Items {
			if p.CreationTimestamp.After(latestPod.CreationTimestamp.Time) {
				latestPod = p
			}
		}
		log.Info("Found latest pod", "pod", latestPod.Name)

		s, err := clientset.CoreV1().Pods(o.namespace).GetLogs(latestPod.Name, &v1.PodLogOptions{
			Container: o.container,
			Follow: true,
		}).Stream(context.TODO())

		if err != nil {
			log.Error(err, "There was a problem streaming the logs")
		}

		// Create a really large buffer
		buffer := make([]byte, 10*1e6)
		for ;; {
			numRead, err := s.Read(buffer)

			lines := strings.Split(string(buffer[0:numRead]), "\n")

			log.Info("Read logs", "numBytes", numRead, "numLines", len(lines), "pod", latestPod.Name)
			for _, l := range lines {
				fmt.Printf("%v\n", l)
			}

			if err == io.EOF {
				log.Info("Stream terminated")
				break
			}
		}

		time.Sleep(10 * time.Second)
	}

	return nil
}

func main() {
	rootCmd.Execute()
}
