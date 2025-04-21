package routers

import (
	"encoding/json"
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

	resourceStore, err := resourceStore.NewPostgresResourceStoreWithJournal[models.EmployeeResource](
		employeeService.Configuration,
		employeeService.Logger)
	if err != nil {
		employeeService.Logger.Println("Error creating PostgresResourceStoreWithJournal:", err)
		return nil
	}

	NounRouter := &NounRouter{
		ServiceBase:   employeeService,
		ResourceStore: resourceStore,
	}
	NounRouter.SetupRoutes()

	return NounRouter
}

func (s *NounRouter) SetupRoutes() {
	// setup auth model to allow both machine (ie. other services) to all and user access to their own
	//
	authModel, err := s.NewAuthModel(security.REALM_MEMBER, security.MATCHING_IDENTITY, security.ONE_DAY, nil)
	if err != nil {
		s.Logger.Fatalf("Failed to initialize AuthModel in NounRouter: %v", err)
	}
	err = authModel.AddPolicy(security.REALM_MACHINE, security.VALID_IDENTITY, security.ONE_HOUR, nil)
	if err != nil {
		s.Logger.Fatalf("Failed to initialize AuthModel in NounRouter: %v", err)
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
}

func (s *NounRouter) GetEmployeeById(w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	urlIdentity := params["identityId"]
	urlEmployee := params["employeeId"]

	var Employee models.EmployeeResource
	status, errmsg := s.ResourceStore.GetById(urlEmployee, urlIdentity, &Employee) // TODO: this should use owner as well
	if status != constants.RESOURCE_OK_CODE {
		s.Logger.Info("GetById failed in GetEmployeeById with: ", errmsg)
		s.WriteHttpError(w, status, errmsg)
		return
	}

	jsonResults, errmsg := json.Marshal(Employee)
	if errmsg != nil {
		s.Logger.Info("Failed to convert employee to json: ", errmsg)
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
		s.Logger.Info("Failed to convert employee to json: ", err)
		s.WriteHttpError(w, constants.RESOURCE_INTERNAL_ERROR_CODE, errmsg)
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

	var Employee models.Employee
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&Employee)
	if err != nil {
		s.Logger.Info("Failed  in CreateEmployeeto read http body: ", err)
		s.WriteHttpError(w, constants.RESOURCE_BAD_REQUEST_CODE, err)
		return
	}

	// create EmployeeResource from Employee
	var EmployeeResource = models.EmployeeResource{
		ResourceBase: resourceStore.ResourceBase{OwnerId: urlIdentity}, Employee: Employee}

	//	authToken := r.Header.Get("X-AuthToken")
	// OR

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
		s.Logger.Info("Failed in CreateEmmployee to json marshall employee resource: ", errmsg)
		s.WriteHttpError(w, constants.RESOURCE_INTERNAL_ERROR_CODE, errmsg)
		return
	}

	s.WriteHttpOK(w, jsonResults)
}

func (s *NounRouter) UpdateEmployeeById(w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	urlIdentity := params["identityId"]
	employeeId := params["employeeId"]

	var EmployeeResource models.EmployeeResource
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&EmployeeResource)
	if err != nil {
		s.Logger.Info("Failed in UpdateEmployeeId to read http body: ", err)
		s.WriteHttpError(w, constants.RESOURCE_BAD_REQUEST_CODE, err)
		return
	}

	//	authToken := r.Header.Get("X-AuthToken")
	// OR

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
		s.Logger.Info("Failed in UpdateEmmployee to json marshall employee resource: ", errmsg)
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
	status, errmsg := s.ResourceStore.GetById(urlEmployeeId, urlIdentityId, &Employee) // TODO: this should use owner as well
	if status != constants.RESOURCE_OK_CODE {
		s.Logger.Info("GetById failed in DeleteEmployeeById with: ", errmsg)
		s.WriteHttpError(w, status, errmsg)
		return
	}

	//	authToken := r.Header.Get("X-AuthToken")
	// OR

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
		s.Logger.Info("Failed in DeleteEmmployeeById to json marshall employee resource: ", errmsg)
		s.WriteHttpError(w, constants.RESOURCE_INTERNAL_ERROR_CODE, errmsg)
		return
	}

	s.WriteHttpOK(w, jsonResults)
}
