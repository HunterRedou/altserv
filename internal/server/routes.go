package server

// Route patterns are shared by the router and tests so a typo cannot silently
// unregister an endpoint.
const (
	RouteApp = "/app/"

	RouteReset         = "POST /admin/reset"
	RouteCreateTenant  = "POST /api/tenants"
	RouteLogin         = "POST /api/login"
	RouteLogout        = "POST /api/logout"
	RouteMe            = "GET /api/me"
	RouteUsers         = "GET /api/users"
	RouteCreateUser    = "POST /api/users"
	RoutePatients      = "GET /api/patients"
	RouteCreatePatient = "POST /api/patients"
	RoutePatient       = "GET /api/patients/{id}"
	RouteUpdatePatient = "PATCH /api/patients/{id}"

	RoutePatientAssignments = "GET /api/patients/{id}/assignments"
	RouteCreateAssignment   = "POST /api/patients/{id}/assignments"
	RouteAssignments        = "GET /api/assignments"
	RouteRevokeAssignment   = "POST /api/assignments/{id}/revoke"

	RouteServiceCatalogs       = "GET /api/service-catalogs"
	RouteServiceCatalog        = "GET /api/service-catalogs/{id}"
	RouteServiceDefinitions    = "GET /api/service-definitions"
	RouteServiceDefinition     = "GET /api/service-definitions/{id}"
	RouteServiceContracts      = "GET /api/service-contracts"
	RouteCreateServiceContract = "POST /api/service-contracts"
	RouteServiceContract       = "GET /api/service-contracts/{id}"

	RoutePatientActivities = "GET /api/patients/{id}/activities"
	RouteCreateActivity    = "POST /api/patients/{id}/activities"
	RouteActivities        = "GET /api/activities"
	RouteActivity          = "GET /api/activities/{id}"

	RouteActivityRules      = "GET /api/activity-rules"
	RouteCreateActivityRule = "POST /api/activity-rules"

	RoutePatientTemplates = "GET /api/patients/{id}/templates"
	RouteCreateTemplate   = "POST /api/patients/{id}/templates"
	RouteTemplate         = "GET /api/templates/{id}"
	RouteUpdateTemplate   = "PATCH /api/templates/{id}"
	RouteArchiveTemplate  = "POST /api/templates/{id}/archive"

	RoutePatientVisits   = "GET /api/patients/{id}/visits"
	RouteCreateVisit     = "POST /api/patients/{id}/visits"
	RouteVisit           = "GET /api/visits/{id}"
	RouteRescheduleVisit = "PATCH /api/visits/{id}"
	RouteStartVisit      = "POST /api/visits/{id}/start"
	RouteCompleteVisit   = "POST /api/visits/{id}/complete"
	RouteCancelVisit     = "POST /api/visits/{id}/cancel"
	RouteCorrectVisit    = "POST /api/visits/{id}/correct"
)
