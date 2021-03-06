package main

import (
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"

	"code.cloudfoundry.org/lager"
	"github.com/starkandwayne/rabbitmq-metrics-emitter/cfclient"
	"github.com/starkandwayne/rabbitmq-metrics-emitter/config"
	"github.com/starkandwayne/rabbitmq-metrics-emitter/forwarder"
	"github.com/starkandwayne/rabbitmq-metrics-emitter/management"
)

func main() {
	logger := lager.NewLogger("rabbitmq-metrics-emitter")
	logger.RegisterSink(lager.NewWriterSink(os.Stdout, lager.DEBUG))
	logger.RegisterSink(lager.NewWriterSink(os.Stderr, lager.ERROR))

	if len(os.Args) < 2 {
		logger.Fatal("Missing argument - specify path to config file", errors.New("Missing config file path"))
	}

	configFilePath := os.Args[1]

	config, err := config.LoadConfig(configFilePath)
	if err != nil {
		logger.Fatal("Reading config file", err, lager.Data{
			"emitter-config-file-path": configFilePath,
		})
	}

	metricsClient, err := forwarder.NewMetricForwarder(logger, &config)

	if err != nil {
		logger.Fatal("Couldn't create metric-forwarder", err)
	}
	cfClient, err := cfclient.NewCfClient(&config)
	if err != nil {
		logger.Fatal("Couldn't create cfClient", err)
	}

	managementClient, err := management.NewManagementClient(logger, &config)
	if err != nil {
		logger.Fatal("Couldn't create RMQ Management client", err)
	}

	ticker := time.NewTicker(5 * time.Second)
	quit := make(chan struct{})

	sigs := make(chan os.Signal, 1)
	done := make(chan bool, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		for {
			select {
			case <-ticker.C:
				vhosts, err := managementClient.GetVhosts()
				if err != nil {
					logger.Error("Couldn't get vhosts", err)
				}
				instanceIds := []string{}
				for _, host := range vhosts {
					instanceIds = append(instanceIds, host.Name)
				}
				bindings, err := cfClient.AllBindings(instanceIds)
				if err != nil {
					logger.Error("Couldn't get bindings", err)
				}
				for _, binding := range bindings {
					queues, err := managementClient.GetQueues(binding.ServiceInstanceGUID)
					if err != nil {
						logger.Error("Couldn't get queues", err)
					}
					for _, info := range queues {
						metricsClient.EmitMetric(binding.AppGUID, &info)
					}
				}
			case <-quit:
				ticker.Stop()
				return
			}
		}
	}()
	go func() {
		<-sigs
		done <- true
	}()
	<-done
	close(quit)
}
