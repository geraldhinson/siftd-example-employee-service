package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/geraldhinson/siftd-base/pkg/constants"
	"github.com/geraldhinson/siftd-base/pkg/serviceBase"
	"github.com/geraldhinson/siftd-example-employee-service/routers"
)

func main() {
	// call setup to get the service base (logging, config, routing) and a keystore
	employeeService := serviceBase.NewServiceBase()
	if employeeService == nil {
		fmt.Println("Failed to validate configuration and listen. Shutting down.")
		return
	}

	NounRouter := routers.NewNounRouter(employeeService)
	if NounRouter == nil {
		employeeService.Logger.Fatalf("Failed to create noun api server. Shutting down.")
		return
	}

	NounJournalRouter := routers.NewNounJournalRouter(employeeService)
	if NounJournalRouter == nil {
		employeeService.Logger.Fatalf("Failed to create journal api server. Shutting down.")
		return
	}

	HealthCheckRouter := routers.NewHealthCheckRouter(employeeService)
	if HealthCheckRouter == nil {
		employeeService.Logger.Fatalf("Failed to create health check api server. Shutting down.")
		return
	}

	// setup
	listenAddress := employeeService.Configuration.GetString(constants.LISTEN_ADDRESS)
	if listenAddress == "" {
		employeeService.Logger.Fatalf("Unable to retrieve listen address and port. Shutting down.")
		return
	}

	// if we are running on localhost, we can add a fake identity service for testing (id is hardcoded in FakeKeyStore.go)
	if strings.Contains(listenAddress, "localhost") {
		FakeIdentityServiceRouter := routers.NewFakeIdentityServiceRouter(employeeService)
		if FakeIdentityServiceRouter == nil {
			employeeService.Logger.Fatalf("Failed to create fake identity service api server (for testing only). Shutting down.")
			return
		}
	}

	employeeService.Logger.Printf("This service is listening on: %s", listenAddress)
	http.ListenAndServe(listenAddress, employeeService.Router)

}
