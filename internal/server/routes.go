package server

// Route patterns are shared by the router and tests so a typo cannot silently
// unregister an endpoint.
const (
	RouteApp = "/app/"

	// Operational endpoints are public so a load balancer and Prometheus can
	// reach them without a user session. They never expose tenant data.
	RouteHealth  = "GET /healthz"
	RouteReady   = "GET /readyz"
	RouteMetrics = "GET /metrics"

	RouteReset          = "POST /admin/reset"
	RouteCreateTenant   = "POST /api/tenants"
	RouteLogin          = "POST /api/login"
	RouteLoginMFA       = "POST /api/login/mfa"
	RouteLogout         = "POST /api/logout"
	RouteMe             = "GET /api/me"
	RouteUsers          = "GET /api/users"
	RouteCreateUser     = "POST /api/users"
	RouteUpdateUserRole = "PATCH /api/users/{id}"
	RouteAuditEvents    = "GET /api/audit-events"

	RouteMFAEnroll        = "POST /api/mfa/enroll"
	RouteMFAConfirm       = "POST /api/mfa/confirm"
	RouteMFADisable       = "POST /api/mfa/disable"
	RouteMFARecoveryCodes = "POST /api/mfa/recovery-codes"

	RoutePasswordResetRequest = "POST /api/password-reset/request"
	RoutePasswordResetConfirm = "POST /api/password-reset/confirm"
	RouteUserPasswordReset    = "POST /api/users/{id}/password-reset"
	RouteDisableUser          = "POST /api/users/{id}/disable"
	RouteEnableUser           = "POST /api/users/{id}/enable"

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

	RouteVisitReports          = "GET /api/visits/{id}/reports"
	RouteCreateReport          = "POST /api/visits/{id}/reports"
	RoutePatientReports        = "GET /api/patients/{id}/reports"
	RouteReport                = "GET /api/reports/{id}"
	RouteUpdateReport          = "PATCH /api/reports/{id}"
	RouteSubmitReport          = "POST /api/reports/{id}/submit"
	RouteApproveReport         = "POST /api/reports/{id}/approve"
	RouteReturnReport          = "POST /api/reports/{id}/return"
	RouteReadyForBillingReport = "POST /api/reports/{id}/ready-for-billing"
	RouteReportTransitions     = "GET /api/reports/{id}/transitions"

	RouteReportBilling     = "GET /api/reports/{id}/billing"
	RouteGenerateBilling   = "POST /api/reports/{id}/billing"
	RouteReportBillingRuns = "GET /api/reports/{id}/billing-runs"
	RouteBillingRun        = "GET /api/billing-runs/{id}"
	RouteApproveBillingRun = "POST /api/billing-runs/{id}/approve"
	RouteVoidBillingRun    = "POST /api/billing-runs/{id}/void"
	RouteExportBillingCSV  = "GET /api/billing-runs/{id}/export.csv"
	RouteExportBillingPDF  = "GET /api/billing-runs/{id}/export.pdf"
	RouteBillingRules      = "GET /api/billing-rules"
	RouteCreateBillingRule = "POST /api/billing-rules"
	RouteBillingTransmit   = "GET /api/billing-transmission"

	RouteBreakGlassGrants       = "GET /api/break-glass/grants"
	RouteCreateBreakGlassGrant  = "POST /api/break-glass/grants"
	RouteApproveBreakGlassGrant = "POST /api/break-glass/grants/{id}/approve"
	RouteDenyBreakGlassGrant    = "POST /api/break-glass/grants/{id}/deny"
	RouteRevokeBreakGlassGrant  = "POST /api/break-glass/grants/{id}/revoke"
	RouteBreakGlassPatient      = "GET /api/break-glass/grants/{id}/patient"
)
