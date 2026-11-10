package server

import "net/http"

// Handlers is the set of HTTP handlers the router wires together.
// It keeps the server package independent of the main package.
type Handlers struct {
	App    http.Handler
	Reset  http.HandlerFunc
	Login  http.HandlerFunc
	Logout http.HandlerFunc
	Me     http.HandlerFunc
	Users  http.HandlerFunc
	Create http.HandlerFunc
	Tenant http.HandlerFunc

	PatientCreate http.HandlerFunc
	PatientList   http.HandlerFunc
	PatientGet    http.HandlerFunc
	PatientUpdate http.HandlerFunc

	AssignmentCreate        http.HandlerFunc
	AssignmentListByPatient http.HandlerFunc
	AssignmentList          http.HandlerFunc
	AssignmentRevoke        http.HandlerFunc

	CatalogList           http.HandlerFunc
	CatalogGet            http.HandlerFunc
	DefinitionSearch      http.HandlerFunc
	DefinitionGet         http.HandlerFunc
	ContractList          http.HandlerFunc
	ContractGet           http.HandlerFunc
	ContractCreate        http.HandlerFunc
	ActivityCreate        http.HandlerFunc
	ActivityList          http.HandlerFunc
	ActivityListByPatient http.HandlerFunc
	ActivityGet           http.HandlerFunc
	ActivityRuleList      http.HandlerFunc
	ActivityRuleCreate    http.HandlerFunc

	TemplateCreate        http.HandlerFunc
	TemplateListByPatient http.HandlerFunc
	TemplateGet           http.HandlerFunc
	TemplateUpdate        http.HandlerFunc
	TemplateArchive       http.HandlerFunc

	VisitCreate        http.HandlerFunc
	VisitListByPatient http.HandlerFunc
	VisitGet           http.HandlerFunc
	VisitReschedule    http.HandlerFunc
	VisitStart         http.HandlerFunc
	VisitComplete      http.HandlerFunc
	VisitCancel        http.HandlerFunc
	VisitCorrect       http.HandlerFunc

	Auth      func(http.Handler) http.Handler
	CSRF      func(http.Handler) http.Handler
	RequestID func(http.Handler) http.Handler
	Global    func(http.Handler) http.Handler
}

// NewRouter builds the route table. Public endpoints are registered directly;
// everything that needs a session runs Auth before CSRF so the CSRF middleware
// can compare the header with the token stored in the session.
func NewRouter(h Handlers) http.Handler {
	mux := http.NewServeMux()

	if h.App != nil {
		mux.Handle(RouteApp, h.App)
	}
	mux.HandleFunc(RouteReset, h.Reset)
	mux.HandleFunc(RouteCreateTenant, h.Tenant)
	mux.HandleFunc(RouteLogin, h.Login)

	protected := func(handler http.HandlerFunc) http.Handler {
		return h.Auth(h.CSRF(handler))
	}
	mux.Handle(RouteLogout, protected(h.Logout))
	mux.Handle(RouteMe, protected(h.Me))
	mux.Handle(RouteUsers, protected(h.Users))
	mux.Handle(RouteCreateUser, protected(h.Create))
	mux.Handle(RoutePatients, protected(h.PatientList))
	mux.Handle(RouteCreatePatient, protected(h.PatientCreate))
	mux.Handle(RoutePatient, protected(h.PatientGet))
	mux.Handle(RouteUpdatePatient, protected(h.PatientUpdate))
	mux.Handle(RoutePatientAssignments, protected(h.AssignmentListByPatient))
	mux.Handle(RouteCreateAssignment, protected(h.AssignmentCreate))
	mux.Handle(RouteAssignments, protected(h.AssignmentList))
	mux.Handle(RouteRevokeAssignment, protected(h.AssignmentRevoke))
	mux.Handle(RouteServiceCatalogs, protected(h.CatalogList))
	mux.Handle(RouteServiceCatalog, protected(h.CatalogGet))
	mux.Handle(RouteServiceDefinitions, protected(h.DefinitionSearch))
	mux.Handle(RouteServiceDefinition, protected(h.DefinitionGet))
	mux.Handle(RouteServiceContracts, protected(h.ContractList))
	mux.Handle(RouteCreateServiceContract, protected(h.ContractCreate))
	mux.Handle(RouteServiceContract, protected(h.ContractGet))
	mux.Handle(RoutePatientActivities, protected(h.ActivityListByPatient))
	mux.Handle(RouteCreateActivity, protected(h.ActivityCreate))
	mux.Handle(RouteActivities, protected(h.ActivityList))
	mux.Handle(RouteActivity, protected(h.ActivityGet))
	mux.Handle(RouteActivityRules, protected(h.ActivityRuleList))
	mux.Handle(RouteCreateActivityRule, protected(h.ActivityRuleCreate))
	mux.Handle(RoutePatientTemplates, protected(h.TemplateListByPatient))
	mux.Handle(RouteCreateTemplate, protected(h.TemplateCreate))
	mux.Handle(RouteTemplate, protected(h.TemplateGet))
	mux.Handle(RouteUpdateTemplate, protected(h.TemplateUpdate))
	mux.Handle(RouteArchiveTemplate, protected(h.TemplateArchive))
	mux.Handle(RoutePatientVisits, protected(h.VisitListByPatient))
	mux.Handle(RouteCreateVisit, protected(h.VisitCreate))
	mux.Handle(RouteVisit, protected(h.VisitGet))
	mux.Handle(RouteRescheduleVisit, protected(h.VisitReschedule))
	mux.Handle(RouteStartVisit, protected(h.VisitStart))
	mux.Handle(RouteCompleteVisit, protected(h.VisitComplete))
	mux.Handle(RouteCancelVisit, protected(h.VisitCancel))
	mux.Handle(RouteCorrectVisit, protected(h.VisitCorrect))

	var handler http.Handler = mux
	if h.Global != nil {
		handler = h.Global(handler)
	}
	if h.RequestID != nil {
		handler = h.RequestID(handler)
	}
	return handler
}
