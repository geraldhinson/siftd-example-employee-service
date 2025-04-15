package main

import (
	"fmt"
	"strings"

	"github.com/geraldhinson/siftd-base/pkg/constants"
	"github.com/geraldhinson/siftd-base/pkg/helpers"
	"github.com/geraldhinson/siftd-base/pkg/security"
	"github.com/geraldhinson/siftd-base/pkg/serviceBase"
	"github.com/geraldhinson/siftd-example-employee-service/models"
	"github.com/geraldhinson/siftd-example-employee-service/routers"
)

func main() {
	// call setup to get the service base (logging, config, routing) and a keystore
	employeeService := serviceBase.NewServiceBase()
	if employeeService == nil {
		fmt.Println("Failed to validate configuration and listen. Shutting down.")
		return
	}

	// the only router we need to write is the noun router
	NounRouter := routers.NewNounRouter(employeeService)
	if NounRouter == nil {
		employeeService.Logger.Fatalf("Failed to create noun api server. Shutting down.")
		return
	}

	// here we use default implementation, but must pass auth model
	NounJournalRouter := helpers.NewNounJournalRouter[models.EmployeeResource](employeeService, security.REALM_MACHINE, security.VALID_IDENTITY, security.ONE_HOUR, nil)
	if NounJournalRouter == nil {
		employeeService.Logger.Fatalf("Failed to create journal api server. Shutting down.")
		return
	}

	// here we use default implementation, but must pass auth model
	HealthCheckRouter := helpers.NewNounHealthCheckRouter[models.EmployeeResource](employeeService, security.NO_REALM, security.NO_AUTH, security.NO_EXPIRY, nil)
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
		FakeIdentityServiceRouter := helpers.NewFakeIdentityServiceRouter(employeeService, security.NO_REALM, security.NO_AUTH, security.NO_EXPIRY, nil)
		if FakeIdentityServiceRouter == nil {
			employeeService.Logger.Fatalf("Failed to create fake identity service api server (for testing only). Shutting down.")
			return
		}
	}

	employeeService.ListenAndServe()
}
