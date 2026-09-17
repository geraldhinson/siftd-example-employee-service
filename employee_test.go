package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/geraldhinson/siftd-base/pkg/constants"
	"github.com/geraldhinson/siftd-base/pkg/helpers"
	"github.com/geraldhinson/siftd-base/pkg/security"
	"github.com/geraldhinson/siftd-base/pkg/serviceBase"
	"github.com/geraldhinson/siftd-example-employee-service/models"
	"github.com/geraldhinson/siftd-example-employee-service/routers"
	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5"
	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/spf13/viper"
)

const (
	testDatabaseName = "EmployeeTest"
	testOwnerID      = "GUID-fake-member-GUID"
	otherOwnerID     = "another-test-owner"
	employeesPath    = "/v1/identities/" + testOwnerID + "/employees"
	childModeEnv     = "EMPLOYEE_TEST_CHILD_MODE"
	shutdownMarker   = "EMPLOYEE_TEST_MAIN_RETURNED"
)

var (
	testDB         *pgx.Conn
	testServer     *httptest.Server
	testService    *serviceBase.ServiceBase
	testLogs       *logtest.Hook
	testClient     = &http.Client{Timeout: 15 * time.Second}
	testDSN        string
	testConfigPath string
	userToken      string
	machineToken   string
)

func TestMain(m *testing.M) {
	// Subprocesses bypass suite initialization and its database lock.
	if mode := os.Getenv(childModeEnv); mode != "" {
		runChild(mode)
		return
	}
	os.Exit(runTests(m))
}

func runTests(m *testing.M) (exitCode int) {
	exitCode = 1

	fail := func(err error) {
		fmt.Fprintln(os.Stderr, "Test setup failed:", err)
	}

	var err error
	testConfigPath, err = filepath.Abs("app.env")
	if err != nil {
		fail(err)
		return
	}

	config := viper.New()
	config.SetConfigFile(testConfigPath)
	config.AutomaticEnv()
	if err := config.ReadInConfig(); err != nil {
		fail(err)
		return
	}

	testDSN = config.GetString("TEST_DB_CONNECTSTRING")
	if strings.TrimSpace(testDSN) == "" {
		fail(errors.New("TEST_DB_CONNECTSTRING is required"))
		return
	}

	// Reject an unintended database before connecting or deleting anything.
	dbConfig, err := pgx.ParseConfig(testDSN)
	if err != nil {
		fail(err)
		return
	}
	if dbConfig.Database != testDatabaseName {
		fail(fmt.Errorf(
			"refusing test database %q; expected %q",
			dbConfig.Database, testDatabaseName,
		))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	testDB, err = pgx.ConnectConfig(ctx, dbConfig)
	cancel()
	if err != nil {
		fail(fmt.Errorf("connect to test database: %w", err))
		return
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := testDB.Close(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "Close test database:", err)
			exitCode = 1
		}
	}()

	if err := verifyTestDatabase(); err != nil {
		fail(err)
		return
	}

	// Cooperating copies of this suite cannot reset the same DB concurrently.
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	var locked bool
	err = testDB.QueryRow(
		ctx, "SELECT pg_try_advisory_lock(8882, 1)",
	).Scan(&locked)
	cancel()
	if err != nil {
		fail(err)
		return
	}
	if !locked {
		fail(errors.New("another test run is using EmployeeTest"))
		return
	}
	// Closing testDB releases the session-level advisory lock.

	defer func() {
		if err := resetTables(); err != nil {
			fmt.Fprintln(os.Stderr, "Final database cleanup failed:", err)
			exitCode = 1
		}
	}()

	keyDirectory, err := os.MkdirTemp("", "employee-test-keys-")
	if err != nil {
		fail(err)
		return
	}
	defer os.RemoveAll(keyDirectory)

	for name, value := range map[string]string{
		"SIFTD_ENV_FILE": testConfigPath,
		"RESDIR_PATH":    keyDirectory,
		"PORT":           "",
	} {
		if err := os.Setenv(name, value); err != nil {
			fail(err)
			return
		}
	}

	testServer = httptest.NewUnstartedServer(nil)
	defer testServer.Close()

	baseURL := "http://" + testServer.Listener.Addr().String()

	viper.Reset()
	defer viper.Reset()
	viper.Set("DB_CONNECTSTRING", testDSN)
	viper.Set("LISTEN_ADDRESS", baseURL)
	viper.Set("IDENTITY_SERVICE", baseURL)
	viper.Set("RESDIR_PATH", keyDirectory)

	testService = serviceBase.NewServiceBase()
	if testService == nil {
		fail(errors.New("could not construct ServiceBase"))
		return
	}
	testLogs = logtest.NewLocal(testService.Logger)

	nounRouter := routers.NewNounRouter(testService)
	if nounRouter == nil {
		fail(errors.New("could not construct noun router"))
		return
	}
	defer nounRouter.ResourceStore.Close()

	if helpers.NewFakeIdentityServiceRouter(
		testService,
		security.NO_REALM,
		security.NO_AUTH,
		security.NO_EXPIRY,
		nil,
	) == nil {
		fail(errors.New("could not construct fake identity router"))
		return
	}

	testServer.Config.Handler = testService.Router
	testServer.Start()

	// Runs before the deferred store close.
	defer testServer.Close()

	userToken, err = fetchToken("/v1/createFakeUserToken")
	if err != nil {
		fail(err)
		return
	}
	machineToken, err = fetchToken("/v1/createFakeMachineToken")
	if err != nil {
		fail(err)
		return
	}

	exitCode = m.Run()
	return
}

func verifyTestDatabase() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var name string
	if err := testDB.QueryRow(ctx, "SELECT current_database()").Scan(&name); err != nil {
		return err
	}
	if name != testDatabaseName {
		return fmt.Errorf("connected to %q instead of %q", name, testDatabaseName)
	}

	for _, query := range []string{
		`SELECT "Id", "OwnerId", "Version", "UpdatedAt", "Deleted", "Resource"
		 FROM public."Resources" LIMIT 0`,
		`SELECT "Clock", "Resource", "UpdatedAt", "PartitionName"
		 FROM public."Journal" LIMIT 0`,
	} {
		rows, err := testDB.Query(ctx, query)
		if err != nil {
			return fmt.Errorf(
				"required test schema unavailable; provision EmployeeTest using the SQL template: %w",
				err,
			)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	return nil
}

func resetTables() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tx, err := testDB.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, `DELETE FROM public."Journal"`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM public."Resources"`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func beginTest(t *testing.T) {
	t.Helper()

	// No t.Parallel: tests reset the same database.
	if err := resetTables(); err != nil {
		t.Fatalf("reset database: %v", err)
	}
	testLogs.Reset()
}

// Snapshot both tables, including resource contents, versions and journal rows.
// Identity-sequence values are deliberately not included: DELETE doesn't reset them.
func databaseState(t *testing.T) [2]string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var state [2]string
	queries := []string{
		`SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY r."Id"), '[]'::jsonb)::text
		 FROM public."Resources" r`,
		`SELECT COALESCE(
		     jsonb_agg(to_jsonb(j) ORDER BY j."Clock", j."PartitionName"),
		     '[]'::jsonb
		 )::text FROM public."Journal" j`,
	}
	for i, query := range queries {
		if err := testDB.QueryRow(ctx, query).Scan(&state[i]); err != nil {
			t.Fatal(err)
		}
	}
	return state
}

func requireUnchanged(t *testing.T, before [2]string) {
	t.Helper()
	if after := databaseState(t); after != before {
		t.Fatalf("request changed database\nbefore: %v\nafter: %v", before, after)
	}
}

func requireLog(t *testing.T, hook *logtest.Hook, level logrus.Level, fragment string) {
	t.Helper()
	for _, entry := range hook.AllEntries() {
		if entry.Level == level && strings.Contains(entry.Message, fragment) {
			return
		}
	}
	t.Fatalf("missing %s log containing %q", level, fragment)
}

func callAPI(method, path, token string, body []byte) (int, []byte, error) {
	request, err := http.NewRequest(
		method, testServer.URL+path, bytes.NewReader(body),
	)
	if err != nil {
		return 0, nil, err
	}
	if token != "" {
		// Current Base expects the raw JWT, matching the Postman collection.
		request.Header.Set("Authorization", token)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := testClient.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()

	data, err := io.ReadAll(response.Body)
	return response.StatusCode, data, err
}

func fetchToken(path string) (string, error) {
	status, body, err := callAPI(http.MethodPost, path, "", nil)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(body))
	if status != http.StatusOK || token == "" {
		return "", fmt.Errorf("%s: status %d, body %q", path, status, body)
	}
	return token, nil
}

func requestAPI(
	t *testing.T,
	method, path, token string,
	body []byte,
	expectedStatus int,
	result any,
) []byte {
	t.Helper()

	status, data, err := callAPI(method, path, token, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	if status != expectedStatus {
		t.Fatalf(
			"%s %s: expected %d, got %d; body: %s",
			method, path, expectedStatus, status, data,
		)
	}
	if result != nil {
		if err := json.Unmarshal(data, result); err != nil {
			t.Fatalf("decode %s: %v; body: %s", path, err, data)
		}
	}
	return data
}

func jsonBody(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func createEmployee(t *testing.T, token string) models.EmployeeResource {
	t.Helper()

	var employee models.EmployeeResource
	requestAPI(
		t, http.MethodPost, employeesPath, token,
		jsonBody(t, models.Employee{Name: "Alice", Age: 30}),
		http.StatusOK, &employee,
	)
	if employee.Id == "" ||
		employee.Id == testOwnerID ||
		employee.OwnerId != testOwnerID ||
		employee.Version != 1 ||
		employee.Deleted ||
		employee.Employee != (models.Employee{Name: "Alice", Age: 30}) {
		t.Fatalf("unexpected created employee: %+v", employee)
	}
	return employee
}

func TestEmployeeCRUD(t *testing.T) {
	for _, auth := range []struct {
		name, token string
	}{
		{"user", userToken},
		{"machine", machineToken},
	} {
		t.Run(auth.name, func(t *testing.T) {
			beginTest(t)
			created := createEmployee(t, auth.token)
			path := employeesPath + "/" + created.Id

			var fetched models.EmployeeResource
			requestAPI(t, "GET", path, auth.token, nil, 200, &fetched)
			if fetched.Id != created.Id || fetched.Employee != created.Employee {
				t.Fatalf("unexpected GET result: %+v", fetched)
			}

			created.Employee.Age = 31
			var updated models.EmployeeResource
			requestAPI(t, "PUT", path, auth.token, jsonBody(t, created), 200, &updated)
			if updated.Id != created.Id ||
				updated.Employee.Age != 31 ||
				updated.Version != 2 {
				t.Fatalf("unexpected update: %+v", updated)
			}

			var listed []models.EmployeeResource
			requestAPI(t, "GET", employeesPath, auth.token, nil, 200, &listed)
			if len(listed) != 1 ||
				listed[0].Id != created.Id ||
				listed[0].Employee.Age != 31 {
				t.Fatalf("unexpected list: %+v", listed)
			}

			var deleted models.EmployeeResource
			requestAPI(t, "DELETE", path, auth.token, nil, 200, &deleted)
			if deleted.Id != created.Id || !deleted.Deleted || deleted.Version != 3 {
				t.Fatalf("unexpected delete: %+v", deleted)
			}

			body := requestAPI(t, "GET", employeesPath, auth.token, nil, 200, &listed)
			if len(listed) != 0 || strings.TrimSpace(string(body)) != "[]" {
				t.Fatalf("expected empty JSON array, got %s", body)
			}

			// DELETE is soft: the row remains, with three journal entries.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			var resourceCount, journalCount int
			if err := testDB.QueryRow(ctx, `
				SELECT
					(SELECT COUNT(*) FROM public."Resources"),
					(SELECT COUNT(*) FROM public."Journal")
			`).Scan(&resourceCount, &journalCount); err != nil {
				t.Fatal(err)
			}
			if resourceCount != 1 || journalCount != 3 {
				t.Fatalf("resources=%d, journal=%d; expected 1 and 3",
					resourceCount, journalCount)
			}
		})
	}
}

func TestMissingEmployee(t *testing.T) {
	for _, method := range []string{"GET", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			beginTest(t)
			before := databaseState(t)

			body := requestAPI(
				t, method, employeesPath+"/missing-employee", userToken,
				nil, 404, nil,
			)
			if !strings.Contains(string(body), "resource not found") {
				t.Fatalf("unexpected error body: %s", body)
			}

			handler := "GetEmployeeById"
			if method == "DELETE" {
				handler = "DeleteEmployeeById"
			}
			requireLog(t, testLogs, logrus.InfoLevel, "GetById failed in "+handler)
			requireUnchanged(t, before)
		})
	}
}

func TestUpdateRejectsInvalidIdentityOrVersion(t *testing.T) {
	for _, name := range []string{"stale-version", "body-id", "body-owner", "missing-resource"} {
		t.Run(name, func(t *testing.T) {
			beginTest(t)
			employee := createEmployee(t, userToken)
			path := employeesPath + "/" + employee.Id

			switch name {
			case "stale-version":
				var updated models.EmployeeResource
				requestAPI(t, "PUT", path, userToken, jsonBody(t, employee), 200, &updated)
				// Keep the original version for the rejected request.
			case "body-id":
				employee.Id = "different-id"
			case "body-owner":
				employee.OwnerId = otherOwnerID
			case "missing-resource":
				employee.Id = "missing-employee"
				path = employeesPath + "/" + employee.Id
			}
			employee.Employee.Age = 99

			before := databaseState(t)
			testLogs.Reset()

			// Base currently maps stale versions and missing PUT resources to 400.
			requestAPI(t, "PUT", path, userToken, jsonBody(t, employee), 400, nil)

			requireLog(t, testLogs, logrus.InfoLevel, "UpdateResource failed in UpdateEmployee")
			requireUnchanged(t, before)
		})
	}
}

func TestRejectsMalformedJSON(t *testing.T) {
	for _, method := range []string{"POST", "PUT"} {
		for _, name := range []string{"empty", "incomplete", "wrong-type", "garbage", "second-value"} {
			t.Run(method+"/"+name, func(t *testing.T) {
				beginTest(t)
				employee := createEmployee(t, userToken)

				path := employeesPath
				valid := jsonBody(t, models.Employee{Name: "Bob", Age: 40})
				handler := "CreateEmployee"
				if method == "PUT" {
					path += "/" + employee.Id
					valid = jsonBody(t, employee)
					handler = "UpdateEmployeeById"
				}

				var body []byte
				switch name {
				case "empty":
					body = []byte("")
				case "incomplete":
					body = []byte(`{"`)
				case "wrong-type":
					body = []byte(`[]`)
				case "garbage":
					body = append(valid, []byte(" garbage")...)
				case "second-value":
					body = append(valid, []byte(` {}`)...)
				}

				before := databaseState(t)
				testLogs.Reset()
				requestAPI(t, method, path, userToken, body, 400, nil)

				requireLog(t, testLogs, logrus.InfoLevel,
					handler+" failed to unmarshall http body")
				requireUnchanged(t, before)
			})
		}
	}
}

func TestAuthenticationAndOwnership(t *testing.T) {
	for _, name := range []string{"missing-token", "invalid-token", "other-owner"} {
		for _, operation := range []string{"get", "list", "create", "update", "delete"} {
			t.Run(name+"/"+operation, func(t *testing.T) {
				beginTest(t)
				employee := createEmployee(t, userToken)

				owner := testOwnerID
				token := userToken
				status := http.StatusUnauthorized

				switch name {
				case "missing-token":
					token = ""
				case "invalid-token":
					token = "not-a-jwt"
				case "other-owner":
					owner = otherOwnerID
					status = http.StatusForbidden
				}

				base := "/v1/identities/" + owner + "/employees"
				path := base + "/" + employee.Id
				method := "GET"
				var body []byte

				switch operation {
				case "list":
					path = base
				case "create":
					method, path = "POST", base
					body = jsonBody(t, employee.Employee)
				case "update":
					method = "PUT"
					employee.OwnerId = owner
					employee.Employee.Age = 99
					body = jsonBody(t, employee)
				case "delete":
					method = "DELETE"
				}

				before := databaseState(t)
				requestAPI(t, method, path, token, body, status, nil)
				requireUnchanged(t, before)
			})
		}
	}
}

func TestMachineTokenStillRequiresMatchingResourceOwner(t *testing.T) {
	for _, method := range []string{"GET", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			beginTest(t)
			employee := createEmployee(t, userToken)
			before := databaseState(t)

			path := "/v1/identities/" + otherOwnerID + "/employees/" + employee.Id
			requestAPI(t, method, path, machineToken, nil, 404, nil)
			requireUnchanged(t, before)
		})
	}
}

// A transport-independent way to make io.ReadAll fail deterministically.
type failingBody struct{}

func (failingBody) Read([]byte) (int, error) {
	return 0, errors.New("simulated body read failure")
}

func (failingBody) Close() error { return nil }

func TestRequestBodyReadFailure(t *testing.T) {
	for _, method := range []string{"POST", "PUT"} {
		t.Run(method, func(t *testing.T) {
			beginTest(t)
			before := databaseState(t)

			logger := logrus.New()
			logger.SetOutput(io.Discard)
			hook := logtest.NewLocal(logger)

			// A nil store makes accidental continuation fail immediately.
			router := &routers.NounRouter{
				ServiceBase: &serviceBase.ServiceBase{Logger: logger},
			}

			request := httptest.NewRequest(method, employeesPath+"/example", nil)
			request.Body = failingBody{}
			request = mux.SetURLVars(request, map[string]string{
				"identityId": testOwnerID,
				"employeeId": "example",
			})
			response := httptest.NewRecorder()

			handler := "CreateEmployee"
			if method == "POST" {
				router.CreateEmployee(response, request)
			} else {
				handler = "UpdateEmployee"
				router.UpdateEmployeeById(response, request)
			}

			if response.Code != 400 ||
				!strings.Contains(response.Body.String(), "simulated body read failure") {
				t.Fatalf("unexpected response: %d %s", response.Code, response.Body)
			}
			requireLog(t, hook, logrus.InfoLevel, handler+" to read http body")
			requireUnchanged(t, before)
		})
	}
}

// Construct isolated router dependencies without changing global Viper settings.
func isolatedService(t *testing.T) (*serviceBase.ServiceBase, *logtest.Hook) {
	t.Helper()

	config := viper.New()
	if err := config.MergeConfigMap(testService.Configuration.AllSettings()); err != nil {
		t.Fatal(err)
	}

	logger := logrus.New()
	logger.SetOutput(io.Discard)
	hook := logtest.NewLocal(logger)

	return &serviceBase.ServiceBase{
		Configuration: config,
		Logger:        logger,
		Router:        mux.NewRouter(),
		KeyCache:      testService.KeyCache,
	}, hook
}

func TestDatabaseFailureResponses(t *testing.T) {
	for _, operation := range []string{"get", "list", "create", "update", "delete"} {
		t.Run(operation, func(t *testing.T) {
			beginTest(t)
			employee := createEmployee(t, userToken)
			before := databaseState(t)

			service, hook := isolatedService(t)
			router := routers.NewNounRouter(service)
			if router == nil {
				t.Fatal("could not create isolated router")
			}
			t.Cleanup(router.ResourceStore.Close)

			// Simulate an unavailable store without stopping PostgreSQL.
			router.ResourceStore.Close()
			hook.Reset()

			method := "GET"
			path := employeesPath + "/" + employee.Id
			var body []byte
			logFragment := "GetById failed in GetEmployeeById"

			switch operation {
			case "list":
				path = employeesPath
				logFragment = "GetByOwnerId failed in GetEmployeesByOwnerId"
			case "create":
				method, path = "POST", employeesPath
				body = jsonBody(t, employee.Employee)
				logFragment = "CreateResource failed in CreateEmployee"
			case "update":
				method = "PUT"
				body = jsonBody(t, employee)
				logFragment = "UpdateResource failed in UpdateEmployee"
			case "delete":
				method = "DELETE"
				logFragment = "GetById failed in DeleteEmployeeById"
			}

			request := httptest.NewRequest(method, path, bytes.NewReader(body))
			request.Header.Set("Authorization", userToken)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			// Exercise the real authentication middleware and handler.
			service.Router.ServeHTTP(response, request)

			if response.Code != 500 ||
				response.Body.String() != constants.INTERNAL_SERVER_ERROR {
				t.Fatalf("expected generic 500; got %d %q",
					response.Code, response.Body.String())
			}
			requireLog(t, hook, logrus.InfoLevel, logFragment)
			requireUnchanged(t, before)
		})
	}
}

func TestNounRouterRejectsInvalidPoolConfiguration(t *testing.T) {
	beginTest(t)
	before := databaseState(t)

	service, hook := isolatedService(t)
	service.Configuration.Set("NOUNROUTER_MAX_DATABASE_CONNECTIONS", 0)

	if router := routers.NewNounRouter(service); router != nil {
		router.ResourceStore.Close()
		t.Fatal("expected nil router")
	}
	requireLog(t, hook, logrus.ErrorLevel, "Error creating PostgresResourceStoreWithJournal")
	requireUnchanged(t, before)
}

// Subprocess support. main() is the actual employee-service main.
func runChild(mode string) {
	viper.Reset()
	viper.Set("DB_CONNECTSTRING", os.Getenv("EMPLOYEE_TEST_CHILD_DSN"))
	viper.Set("LISTEN_ADDRESS", os.Getenv("EMPLOYEE_TEST_CHILD_URL"))
	viper.Set("IDENTITY_SERVICE", os.Getenv("EMPLOYEE_TEST_CHILD_URL"))
	viper.Set("RESDIR_PATH", os.Getenv("RESDIR_PATH"))
	viper.Set("SERVICE_INSTANCE_NAME", "Employee lifecycle test")
	viper.Set("CALLED_SERVICES", "[]")
	viper.Set("NOUNROUTER_MAX_DATABASE_CONNECTIONS", 1)
	viper.Set("JOURNAL_DB_POOL_MAX_CONNS", 1)
	viper.Set("HEALTH_DB_POOL_MAX_CONNS", 1)
	viper.Set("JOURNAL_PARTITION_NAME", "TEST")
	viper.Set("DEBUGSIFTD_AUTH", 0)

	if mode == "startup-failure" {
		viper.Set("NOUNROUTER_MAX_DATABASE_CONNECTIONS", 0)
	}

	main()

	if mode == "shutdown" {
		fmt.Println(shutdownMarker)

		// Stay alive so the parent can prove connections were closed by
		// shutdown callbacks, rather than merely by process termination.
		var release [1]byte
		_, _ = os.Stdin.Read(release[:])
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type childProcess struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	output *lockedBuffer
	done   chan error
}

func childEnvironment(overrides map[string]string) []string {
	var result []string
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if _, replaced := overrides[key]; !replaced {
			result = append(result, item)
		}
	}
	for key, value := range overrides {
		result = append(result, key+"="+value)
	}
	return result
}

func startChild(t *testing.T, mode string) (*childProcess, string, string) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("lifecycle tests use POSIX signals")
	}

	// Reserve a port, then release it for the production listener.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	baseURL := "http://" + listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	applicationName := fmt.Sprintf("employee-test-%d-%d", os.Getpid(), time.Now().UnixNano())

	childDSN := testDSN

	if strings.HasPrefix(childDSN, "postgres://") ||
		strings.HasPrefix(childDSN, "postgresql://") {
		parsed, err := url.Parse(childDSN)
		if err != nil {
			t.Fatal(err)
		}
		query := parsed.Query()
		query.Set("application_name", applicationName)
		parsed.RawQuery = query.Encode()
		childDSN = parsed.String()
	} else {
		// applicationName is generated above using only letters, digits and hyphens.
		childDSN += " application_name=" + applicationName
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	keyDirectory := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	cmd := exec.CommandContext(ctx, executable)
	cmd.Env = childEnvironment(map[string]string{
		childModeEnv:              mode,
		"EMPLOYEE_TEST_CHILD_DSN": childDSN,
		"EMPLOYEE_TEST_CHILD_URL": baseURL,
		"SIFTD_ENV_FILE":          testConfigPath,
		"RESDIR_PATH":             keyDirectory,
		"PORT":                    "",
	})

	output := &lockedBuffer{}
	cmd.Stdout, cmd.Stderr = output, output

	input, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		_ = input.Close()
		cancel()
		t.Fatal(err)
	}

	child := &childProcess{
		cmd: cmd, input: input, output: output, done: make(chan error, 1),
	}
	go func() {
		child.done <- cmd.Wait()
		close(child.done)
	}()

	// Ensure failures never leave a child service running.
	t.Cleanup(func() {
		_ = child.input.Close()
		cancel()
		select {
		case <-child.done:
		case <-time.After(5 * time.Second):
			t.Error("child process did not terminate")
		}
	})

	return child, baseURL, applicationName
}

func waitForChild(t *testing.T, child *childProcess) error {
	t.Helper()
	select {
	case err := <-child.done:
		return err
	case <-time.After(15 * time.Second):
		t.Fatalf("child exit timed out:\n%s", child.output.String())
		return nil
	}
}

func waitForCondition(t *testing.T, child *childProcess, description string, check func() bool) {
	t.Helper()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		select {
		case err := <-child.done:
			t.Fatalf("child exited while waiting for %s: %v\n%s",
				description, err, child.output.String())
		default:
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s:\n%s", description, child.output.String())
}

func connectionCount(t *testing.T, applicationName string) int {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var count int
	if err := testDB.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM pg_stat_activity
		WHERE datname = current_database()
		  AND application_name = $1
	`, applicationName).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestFatalStartupFailure(t *testing.T) {
	beginTest(t)
	before := databaseState(t)
	child, _, _ := startChild(t, "startup-failure")

	err := waitForChild(t, child)
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 1 {
		t.Fatalf("expected exit code 1, got %v\n%s", err, child.output.String())
	}

	output := child.output.String()
	for _, fragment := range []string{
		"Error creating PostgresResourceStoreWithJournal",
		"Failed to create noun api server",
		"level=fatal",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("missing log %q:\n%s", fragment, output)
		}
	}
	if strings.Contains(output, "starting HTTP server") {
		t.Fatalf("server started after fatal initialization failure:\n%s", output)
	}
	requireUnchanged(t, before)
}

func TestGracefulShutdownClosesDatabasePools(t *testing.T) {
	beginTest(t)
	before := databaseState(t)
	child, baseURL, applicationName := startChild(t, "shutdown")

	probe := &http.Client{Timeout: 500 * time.Millisecond}
	defer probe.CloseIdleConnections()

	waitForCondition(t, child, "healthy HTTP listener", func() bool {
		response, err := probe.Get(baseURL + "/v1/health")
		if err != nil {
			return false
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, response.Body)
		return response.StatusCode == http.StatusOK
	})

	// Noun, journal and health stores each establish one connection.
	if count := connectionCount(t, applicationName); count < 3 {
		t.Fatalf("expected at least three service connections, got %d", count)
	}

	if err := child.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	waitForCondition(t, child, "main to return after shutdown", func() bool {
		return strings.Contains(child.output.String(), shutdownMarker)
	})

	// The child is still alive, blocked on stdin.
	waitForCondition(t, child, "database pools to close", func() bool {
		return connectionCount(t, applicationName) == 0
	})

	if response, err := probe.Get(baseURL + "/v1/health"); err == nil {
		response.Body.Close()
		t.Fatal("HTTP listener still accepts requests after shutdown")
	}

	output := child.output.String()
	if !strings.Contains(output, "shutting down HTTP server") {
		t.Fatalf("missing shutdown log:\n%s", output)
	}
	if strings.Contains(output, "level=fatal") ||
		strings.Contains(output, "HTTP shutdown error") {
		t.Fatalf("unexpected shutdown failure:\n%s", output)
	}

	// Allow the child to exit now that cleanup has been verified.
	_ = child.input.Close()
	if err := waitForChild(t, child); err != nil {
		t.Fatalf("expected successful child exit: %v\n%s", err, child.output.String())
	}
	requireUnchanged(t, before)
}
