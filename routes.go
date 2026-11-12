package main

import (
	"context"
	"net/http"

	"github.com/HunterRedou/altserv/internal/activities"
	"github.com/HunterRedou/altserv/internal/assignments"
	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/billing"
	"github.com/HunterRedou/altserv/internal/breakglass"
	"github.com/HunterRedou/altserv/internal/catalog"
	"github.com/HunterRedou/altserv/internal/patients"
	"github.com/HunterRedou/altserv/internal/reports"
	"github.com/HunterRedou/altserv/internal/retention"
	"github.com/HunterRedou/altserv/internal/server"
	"github.com/HunterRedou/altserv/internal/templates"
	"github.com/HunterRedou/altserv/internal/visits"
)

// routes builds the handler tree. The route table itself lives in
// internal/server so it can be tested without the database.
func (cfg *apiConfig) routes() http.Handler {
	patientHandler := cfg.patientHandler()
	assignmentHandler := cfg.assignmentHandler()
	catalogHandler := cfg.catalogHandler()
	activityHandler := cfg.activityHandler()
	templateHandler := cfg.templateHandler()
	visitHandler := cfg.visitHandler()
	reportHandler := cfg.reportHandler()
	billingHandler := cfg.billingHandler()
	breakGlassHandler := cfg.breakGlassHandler()
	retentionHandler := cfg.retentionHandler()
	auditHandler := cfg.auditHandler()

	return server.NewRouter(server.Handlers{
		App:                     cfg.middlewareMetricsInc(http.StripPrefix("/app", http.FileServer(http.Dir(".")))),
		Health:                  cfg.health.Live,
		Ready:                   cfg.health.Ready,
		Metrics:                 cfg.metricsHandler(),
		Reset:                   cfg.handlerReset,
		Tenant:                  cfg.handlerCreateTenant,
		Login:                   cfg.middlewareLoginRateLimit(cfg.handlerLogin),
		LoginMFA:                cfg.middlewareLoginRateLimit(cfg.handlerLoginMFA),
		Logout:                  cfg.handlerLogout,
		Me:                      cfg.handlerMe,
		Users:                   cfg.handlerGetUsers,
		Create:                  cfg.handlerUser,
		UserUpdateRole:          cfg.handlerUpdateUserRole,
		AuditEvents:             auditHandler.List,
		MFAEnroll:               cfg.handlerMFAEnroll,
		MFAConfirm:              cfg.handlerMFAConfirm,
		MFADisable:              cfg.handlerMFADisable,
		MFARecoveryCodes:        cfg.handlerMFARecoveryCodes,
		PasswordResetRequest:    cfg.middlewarePasswordResetRateLimit(cfg.handlerPasswordResetRequest),
		PasswordResetConfirm:    cfg.middlewarePasswordResetRateLimit(cfg.handlerPasswordResetConfirm),
		UserPasswordReset:       cfg.handlerAdminPasswordReset,
		DisableUser:             cfg.handlerDisableUser,
		EnableUser:              cfg.handlerEnableUser,
		PatientCreate:           patientHandler.Create,
		PatientList:             patientHandler.List,
		PatientGet:              patientHandler.Get,
		PatientUpdate:           patientHandler.Update,
		AssignmentCreate:        assignmentHandler.Create,
		AssignmentListByPatient: assignmentHandler.ListByPatient,
		AssignmentList:          assignmentHandler.List,
		AssignmentRevoke:        assignmentHandler.Revoke,
		CatalogList:             catalogHandler.ListCatalogs,
		CatalogCreate:           catalogHandler.CreateCatalog,
		CatalogGet:              catalogHandler.GetCatalog,
		DefinitionSearch:        catalogHandler.SearchDefinitions,
		DefinitionCreate:        catalogHandler.CreateDefinition,
		DefinitionGet:           catalogHandler.GetDefinition,
		ContractList:            catalogHandler.ListContracts,
		ContractGet:             catalogHandler.GetContract,
		ContractCreate:          catalogHandler.CreateContract,
		ActivityCreate:          activityHandler.Create,
		ActivityList:            activityHandler.List,
		ActivityListByPatient:   activityHandler.ListByPatient,
		ActivityGet:             activityHandler.Get,
		ActivityRuleList:        catalogHandler.ListRules,
		ActivityRuleCreate:      catalogHandler.CreateRule,
		TemplateCreate:          templateHandler.Create,
		TemplateListByPatient:   templateHandler.ListByPatient,
		TemplateGet:             templateHandler.Get,
		TemplateUpdate:          templateHandler.Update,
		TemplateArchive:         templateHandler.Archive,
		VisitCreate:             visitHandler.Create,
		VisitListByPatient:      visitHandler.ListByPatient,
		VisitGet:                visitHandler.Get,
		VisitReschedule:         visitHandler.Reschedule,
		VisitStart:              visitHandler.Start,
		VisitComplete:           visitHandler.Complete,
		VisitCancel:             visitHandler.Cancel,
		VisitCorrect:            visitHandler.Correct,
		ReportCreate:            reportHandler.Create,
		ReportListByVisit:       reportHandler.ListByVisit,
		ReportListByPatient:     reportHandler.ListByPatient,
		ReportGet:               reportHandler.Get,
		ReportUpdate:            reportHandler.Update,
		ReportSubmit:            reportHandler.Submit,
		ReportApprove:           reportHandler.Approve,
		ReportReturn:            reportHandler.Return,
		ReportReadyForBilling:   reportHandler.ReadyForBilling,
		ReportTransitions:       reportHandler.ListTransitions,
		BillingGenerate:         billingHandler.Generate,
		BillingGetActive:        billingHandler.GetActive,
		BillingListByReport:     billingHandler.ListByReport,
		BillingRunGet:           billingHandler.Get,
		BillingApprove:          billingHandler.Approve,
		BillingVoid:             billingHandler.Void,
		BillingExportCSV:        billingHandler.ExportCSV,
		BillingExportPDF:        billingHandler.ExportPDF,
		BillingRuleList:         billingHandler.ListRules,
		BillingRuleCreate:       billingHandler.CreateRule,
		BillingTransmission:     billingHandler.Transmission,
		BreakGlassList:          breakGlassHandler.List,
		BreakGlassRequest:       breakGlassHandler.Request,
		BreakGlassApprove:       breakGlassHandler.Approve,
		BreakGlassDeny:          breakGlassHandler.Deny,
		BreakGlassRevoke:        breakGlassHandler.Revoke,
		BreakGlassPatient:       breakGlassHandler.Patient,
		RetentionPolicyList:     retentionHandler.ListPolicies,
		RetentionPolicyCreate:   retentionHandler.CreatePolicy,
		RetentionPolicyUpdate:   retentionHandler.UpdatePolicy,
		RetentionPolicyConfirm:  retentionHandler.ConfirmPolicy,
		RetentionPolicyDisable:  retentionHandler.DisablePolicy,
		RetentionReview:         retentionHandler.Review,
		LegalHoldList:           retentionHandler.ListLegalHolds,
		LegalHoldCreate:         retentionHandler.CreateLegalHold,
		LegalHoldLift:           retentionHandler.LiftLegalHold,
		DeletionRequestList:     retentionHandler.ListDeletionRequests,
		DeletionRequestCreate:   retentionHandler.CreateDeletionRequest,
		DeletionRequestApprove:  retentionHandler.ApproveDeletionRequest,
		DeletionRequestReject:   retentionHandler.RejectDeletionRequest,
		DeletionRequestCancel:   retentionHandler.CancelDeletionRequest,
		DeletionRequestExecute:  retentionHandler.ExecuteDeletion,
		DeletionRegisterList:    retentionHandler.ListRegister,
		Auth:                    cfg.middlewareAuth,
		CSRF:                    cfg.middlewareCSRF,
		RequestID:               cfg.middlewareRequestID,
		Global:                  cfg.globalMiddleware,
	})
}

// globalMiddleware applies transport-level hardening to every route. RequestID
// runs outside this chain, so the logs and panic reports can correlate events.
func (cfg *apiConfig) globalMiddleware(next http.Handler) http.Handler {
	return cfg.metrics.Middleware(
		cfg.middlewareRecover(
			cfg.middlewareSecurityHeaders(
				cfg.middlewareBodyLimit(
					cfg.middlewareLogging(next),
				),
			),
		),
	)
}

func (cfg *apiConfig) patientHandler() *patients.Handler {
	return patients.NewHandler(cfg.patientService, cfg.withTenant, actorFromContext, requestIDFromContext)
}

func (cfg *apiConfig) assignmentHandler() *assignments.Handler {
	return assignments.NewHandler(cfg.assignmentService, cfg.withTenant, assignmentActorFromContext, requestIDFromContext)
}

func (cfg *apiConfig) catalogHandler() *catalog.Handler {
	return catalog.NewHandler(cfg.catalogService, cfg.withTenant, catalogActorFromContext, requestIDFromContext)
}

func (cfg *apiConfig) activityHandler() *activities.Handler {
	return activities.NewHandler(cfg.activityService, cfg.withTenant, activityActorFromContext, requestIDFromContext)
}

func (cfg *apiConfig) templateHandler() *templates.Handler {
	return templates.NewHandler(cfg.templateService, cfg.withTenant, templateActorFromContext, requestIDFromContext)
}

func (cfg *apiConfig) visitHandler() *visits.Handler {
	return visits.NewHandler(cfg.visitService, cfg.withTenant, visitActorFromContext, requestIDFromContext)
}

func (cfg *apiConfig) reportHandler() *reports.Handler {
	return reports.NewHandler(cfg.reportService, cfg.withTenant, reportActorFromContext, requestIDFromContext)
}

func (cfg *apiConfig) billingHandler() *billing.Handler {
	return billing.NewHandler(cfg.billingService, cfg.withTenant, billingActorFromContext, requestIDFromContext)
}

func (cfg *apiConfig) breakGlassHandler() *breakglass.Handler {
	return breakglass.NewHandler(cfg.breakGlassService, cfg.withTenant, breakGlassActorFromContext, requestIDFromContext)
}

func (cfg *apiConfig) retentionHandler() *retention.Handler {
	return retention.NewHandler(cfg.retentionService, cfg.withTenant, retentionActorFromContext, requestIDFromContext)
}

func (cfg *apiConfig) auditHandler() *audit.Handler {
	service := audit.NewService(audit.NewRepository(), cfg.auditRecorder)
	return audit.NewHandler(service, cfg.withTenant, auditActorFromContext, requestIDFromContext)
}

func actorFromContext(ctx context.Context) (patients.Actor, bool) {
	user, ok := authUserFromContext(ctx)
	if !ok {
		return patients.Actor{}, false
	}
	return patients.Actor{
		UserID:   user.ID,
		TenantID: user.TenantID,
		Role:     user.Role,
	}, true
}

func assignmentActorFromContext(ctx context.Context) (assignments.Actor, bool) {
	user, ok := authUserFromContext(ctx)
	if !ok {
		return assignments.Actor{}, false
	}
	return assignments.Actor{
		UserID:   user.ID,
		TenantID: user.TenantID,
		Role:     user.Role,
	}, true
}

func catalogActorFromContext(ctx context.Context) (catalog.Actor, bool) {
	user, ok := authUserFromContext(ctx)
	if !ok {
		return catalog.Actor{}, false
	}
	return catalog.Actor{
		UserID:   user.ID,
		TenantID: user.TenantID,
		Role:     user.Role,
	}, true
}

func activityActorFromContext(ctx context.Context) (activities.Actor, bool) {
	user, ok := authUserFromContext(ctx)
	if !ok {
		return activities.Actor{}, false
	}
	return activities.Actor{
		UserID:   user.ID,
		TenantID: user.TenantID,
		Role:     user.Role,
	}, true
}

func templateActorFromContext(ctx context.Context) (templates.Actor, bool) {
	user, ok := authUserFromContext(ctx)
	if !ok {
		return templates.Actor{}, false
	}
	return templates.Actor{
		UserID:   user.ID,
		TenantID: user.TenantID,
		Role:     user.Role,
	}, true
}

func visitActorFromContext(ctx context.Context) (visits.Actor, bool) {
	user, ok := authUserFromContext(ctx)
	if !ok {
		return visits.Actor{}, false
	}
	return visits.Actor{
		UserID:   user.ID,
		TenantID: user.TenantID,
		Role:     user.Role,
	}, true
}

func reportActorFromContext(ctx context.Context) (reports.Actor, bool) {
	user, ok := authUserFromContext(ctx)
	if !ok {
		return reports.Actor{}, false
	}
	return reports.Actor{
		UserID:   user.ID,
		TenantID: user.TenantID,
		Role:     user.Role,
	}, true
}

func billingActorFromContext(ctx context.Context) (billing.Actor, bool) {
	user, ok := authUserFromContext(ctx)
	if !ok {
		return billing.Actor{}, false
	}
	return billing.Actor{
		UserID:   user.ID,
		TenantID: user.TenantID,
		Role:     user.Role,
	}, true
}

func breakGlassActorFromContext(ctx context.Context) (breakglass.Actor, bool) {
	user, ok := authUserFromContext(ctx)
	if !ok {
		return breakglass.Actor{}, false
	}
	return breakglass.Actor{
		UserID:   user.ID,
		TenantID: user.TenantID,
		Role:     user.Role,
	}, true
}

func retentionActorFromContext(ctx context.Context) (retention.Actor, bool) {
	user, ok := authUserFromContext(ctx)
	if !ok {
		return retention.Actor{}, false
	}
	return retention.Actor{
		UserID:   user.ID,
		TenantID: user.TenantID,
		Role:     user.Role,
	}, true
}

func auditActorFromContext(ctx context.Context) (audit.Actor, bool) {
	user, ok := authUserFromContext(ctx)
	if !ok {
		return audit.Actor{}, false
	}
	return audit.Actor{
		UserID:   user.ID,
		TenantID: user.TenantID,
		Role:     user.Role,
	}, true
}
