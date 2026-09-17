package routers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/geraldhinson/siftd-base/pkg/constants"
	"github.com/geraldhinson/siftd-base/pkg/resourceStore"
	"github.com/geraldhinson/siftd-base/pkg/security"
	"github.com/geraldhinson/siftd-base/pkg/serviceBase"
	"github.com/geraldhinson/siftd-example-employee-service/models"
	"github.com/gorilla/mux"
)

type NounRouter struct {
	*serviceBase.ServiceBase
	ResourceStore *resourceStore.PostgresResourceStoreWithJournal[models.EmployeeResource]
}

func NewNounRouter(employeeService *serviceBase.ServiceBase) *NounRouter {
	employeeService.Logger.Info("Setting up the noun router")

	store, err := resourceStore.NewPostgresJournaledResourceStore[models.EmployeeResource](
		employeeService.Configuration,
		employeeService.Logger,
		"NOUNROUTER_MAX_DATABASE_CONNECTIONS",
	)
	if err != nil {
		employeeService.Logger.Errorf("Error creating PostgresResourceStoreWithJournal: %v", err)
		return nil
	}

	NounRouter := &NounRouter{
		ServiceBase:   employeeService,
		ResourceStore: store,
	}
	err = NounRouter.SetupRoutes()
	if err != nil {
		store.Close()

		employeeService.Logger.Errorf(
			"noun router - failure detected while setting up routes: %v",
			err,
		)

		return nil
	}

	if err := employeeService.RegisterShutdown(store.Close); err != nil {
		store.Close()

		employeeService.Logger.Errorf(
			"noun router - failed to register store shutdown: %v",
			err,
		)

		return nil
	}

	return NounRouter
}

func (s *NounRouter) SetupRoutes() error {
	// setup auth model to allow both machine (ie. other services) to all and user access to their own
	//
	authModel, err := s.NewAuthModel(security.REALM_MEMBER, security.MATCHING_IDENTITY, security.ONE_DAY, nil)
	if err != nil {
		return fmt.Errorf("Failed to initialize REALM_MEMBER/MATCHING_IDENTITY AuthModel in NounRouter: %w", err)
	}
	err = authModel.AddPolicy(security.REALM_MACHINE, security.VALID_IDENTITY, security.ONE_HOUR, nil)
	if err != nil {
		return fmt.Errorf("Failed to initialize REALM_MACHINE/VALID_IDENTITY AuthModel in NounRouter: %w", err)
	}

	var routeString = "/v1/identities/{identityId}/employees/{employeeId}"
	s.RegisterRoute(constants.HTTP_GET, routeString, authModel, s.GetEmployeeById)

	routeString = "/v1/identities/{identityId}/employees"
	s.RegisterRoute(constants.HTTP_GET, routeString, authModel, s.GetEmployeesByOwnerId)

	routeString = "/v1/identities/{identityId}/employees"
	s.RegisterRoute(constants.HTTP_POST, routeString, authModel, s.CreateEmployee)

	routeString = "/v1/identities/{identityId}/employees/{employeeId}"
	s.RegisterRoute(constants.HTTP_PUT, routeString, authModel, s.UpdateEmployeeById)

	routeString = "/v1/identities/{identityId}/employees/{employeeId}"
	s.RegisterRoute(constants.HTTP_DELETE, routeString, authModel, s.DeleteEmployeeById)

	return nil
}

func (s *NounRouter) GetEmployeeById(w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	urlIdentity := params["identityId"]
	urlEmployee := params["employeeId"]

	var Employee models.EmployeeResource
	status, errmsg := s.ResourceStore.GetById(urlIdentity, urlEmployee, &Employee) // TODO: this should use owner as well
	if status != constants.RESOURCE_OK_CODE {
		s.Logger.Info("GetById failed in GetEmployeeById with: ", errmsg)
		s.WriteHttpError(w, status, errmsg)
		return
	}

	jsonResults, errmsg := json.Marshal(Employee)
	if errmsg != nil {
		s.Logger.Error("GetEmployeeById failed to convert employee to json: ", errmsg)
		s.WriteHttpError(w, constants.RESOURCE_INTERNAL_ERROR_CODE, errmsg)
		return
	}

	s.WriteHttpOK(w, jsonResults)
}

func (s *NounRouter) GetEmployeesByOwnerId(w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	urlIdentity := params["identityId"]

	// create an empty array of models.EmployeeResource
	var Employees []models.EmployeeResource
	status, errmsg := s.ResourceStore.GetByOwnerId(urlIdentity, &Employees)
	if status != constants.RESOURCE_OK_CODE {
		s.Logger.Info("GetByOwnerId failed in GetEmployeesByOwnerId with: ", errmsg)
		s.WriteHttpError(w, status, errmsg)
		return
	}
	jsonResults, err := json.Marshal(Employees)
	if err != nil {
		s.Logger.Error("GetEmployeesByOwnerId failed to convert employees to json: ", err)
		s.WriteHttpError(w, constants.RESOURCE_INTERNAL_ERROR_CODE, err)
		return
	}
	// make empty array if no results found - it's friendlier to the client
	if string(jsonResults) == "null" {
		jsonResults = []byte("[]")
	}

	s.WriteHttpOK(w, jsonResults)
}

func (s *NounRouter) CreateEmployee(w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	urlIdentity := params["identityId"]

	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.Logger.Info("Failed  in CreateEmployee to read http body: ", err)
		s.WriteHttpError(w, constants.RESOURCE_BAD_REQUEST_CODE, err)
		return
	}

	var Employee models.Employee
	if err := json.Unmarshal(body, &Employee); err != nil {
		s.Logger.Info("CreateEmployee failed to unmarshall http body: ", err)
		s.WriteHttpError(w, constants.RESOURCE_BAD_REQUEST_CODE, err)
		return
	}

	// create EmployeeResource from Employee
	var EmployeeResource = models.EmployeeResource{
		ResourceBase: resourceStore.ResourceBase{OwnerId: urlIdentity}, Employee: Employee}

	//	authToken := r.Header.Get("X-AuthToken")
	// OR just use:
	authToken := security.GetAuthHeader(r)

	// create the resource
	resource, status, errmsg := s.ResourceStore.CreateResource(&EmployeeResource, authToken)
	if status != constants.RESOURCE_OK_CODE {
		s.Logger.Info("CreateResource failed in CreateEmployee with: ", errmsg)
		s.WriteHttpError(w, status, errmsg)
		return
	}

	jsonResults, errmsg := json.Marshal(resource)
	if errmsg != nil {
		s.Logger.Error("CreateEmployee failed to marshall employee resource: ", errmsg)
		s.WriteHttpError(w, constants.RESOURCE_INTERNAL_ERROR_CODE, errmsg)
		return
	}

	s.WriteHttpOK(w, jsonResults)
}

func (s *NounRouter) UpdateEmployeeById(w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	urlIdentity := params["identityId"]
	employeeId := params["employeeId"]

	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.Logger.Info("Failed  in UpdateEmployee to read http body: ", err)
		s.WriteHttpError(w, constants.RESOURCE_BAD_REQUEST_CODE, err)
		return
	}

	var EmployeeResource models.EmployeeResource
	if err := json.Unmarshal(body, &EmployeeResource); err != nil {
		s.Logger.Info("UpdateEmployeeById failed to unmarshall http body: ", err)
		s.WriteHttpError(w, constants.RESOURCE_BAD_REQUEST_CODE, err)
		return
	}

	//	authToken := r.Header.Get("X-AuthToken")
	// OR just use:
	authToken := security.GetAuthHeader(r)

	// update the resource
	updatedResource, status, errmsg := s.ResourceStore.UpdateResource(&EmployeeResource, urlIdentity, employeeId, authToken)
	if status != constants.RESOURCE_OK_CODE {
		s.Logger.Info("UpdateResource failed in UpdateEmployee with: ", errmsg)
		s.WriteHttpError(w, status, errmsg)
		return
	}

	jsonResults, errmsg := json.Marshal(updatedResource)
	if errmsg != nil {
		s.Logger.Error("UpdateEmployeeById failed to marshall employee resource: ", errmsg)
		s.WriteHttpError(w, constants.RESOURCE_INTERNAL_ERROR_CODE, errmsg)
		return
	}

	s.WriteHttpOK(w, jsonResults)
}

func (s *NounRouter) DeleteEmployeeById(w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	urlIdentityId := params["identityId"]
	urlEmployeeId := params["employeeId"]

	var Employee models.EmployeeResource
	status, errmsg := s.ResourceStore.GetById(urlIdentityId, urlEmployeeId, &Employee) // TODO: this should use owner as well
	if status != constants.RESOURCE_OK_CODE {
		s.Logger.Info("GetById failed in DeleteEmployeeById with: ", errmsg)
		s.WriteHttpError(w, status, errmsg)
		return
	}

	//	authToken := r.Header.Get("X-AuthToken")
	// OR just use:
	authToken := security.GetAuthHeader(r)

	Employee.Deleted = true
	updatedResource, status, errmsg := s.ResourceStore.UpdateResource(&Employee, urlIdentityId, urlEmployeeId, authToken)
	if status != constants.RESOURCE_OK_CODE {
		s.Logger.Info("UpdateResource failed in DeleteEmployeeById with: ", errmsg)
		s.WriteHttpError(w, status, errmsg)
		return
	}

	jsonResults, errmsg := json.Marshal(updatedResource)
	if errmsg != nil {
		s.Logger.Error("DeleteEmployeeById failed to marshall employee resource: ", errmsg)
		s.WriteHttpError(w, constants.RESOURCE_INTERNAL_ERROR_CODE, errmsg)
		return
	}

	s.WriteHttpOK(w, jsonResults)
}
