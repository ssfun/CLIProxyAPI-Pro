// Package embeddedusage preserves the historical Core integration surface.
// The implementation lives in internal/pro/observability; this package must
// remain a type/function alias layer so upstream seams and backup formats do
// not change during the static modularization.
package embeddedusage

import proobservability "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/observability"

const (
	ProSettingNamespaceRoutingRequestProtection = proobservability.ProSettingNamespaceRoutingRequestProtection
	ProSettingNamespaceProxyPool                = proobservability.ProSettingNamespaceProxyPool
	ProSettingNamespaceOAuthPolicy              = proobservability.ProSettingNamespaceOAuthPolicy
	LegacyProSettingNamespaceOAuthModelPolicy   = proobservability.LegacyProSettingNamespaceOAuthModelPolicy
	XAIQuotaParserVersion                       = proobservability.XAIQuotaParserVersion
)

type (
	Service                        = proobservability.Service
	Server                         = proobservability.Server
	Store                          = proobservability.Store
	Config                         = proobservability.Config
	InsertResult                   = proobservability.InsertResult
	UsageSummary                   = proobservability.UsageSummary
	UsageDatasetState              = proobservability.UsageDatasetState
	UsageResetResult               = proobservability.UsageResetResult
	UsageEventQueryOptions         = proobservability.UsageEventQueryOptions
	UsageEventQueryPage            = proobservability.UsageEventQueryPage
	UsageAggregateBucket           = proobservability.UsageAggregateBucket
	UsageAggregateOptions          = proobservability.UsageAggregateOptions
	AccountUsageDayStat            = proobservability.AccountUsageDayStat
	AccountUsageModelStat          = proobservability.AccountUsageModelStat
	AccountUsageAPIKeyStat         = proobservability.AccountUsageAPIKeyStat
	AccountUsageDetail             = proobservability.AccountUsageDetail
	AccountUsageOptions            = proobservability.AccountUsageOptions
	DeadLetterSample               = proobservability.DeadLetterSample
	QuotaCacheEntry                = proobservability.QuotaCacheEntry
	QuotaCacheStats                = proobservability.QuotaCacheStats
	RoutingCursorState             = proobservability.RoutingCursorState
	RuntimeRequestBucket           = proobservability.RuntimeRequestBucket
	AuthRuntimeStats               = proobservability.AuthRuntimeStats
	MonitoringSettings             = proobservability.MonitoringSettings
	ModelPriceSyncConfig           = proobservability.ModelPriceSyncConfig
	MonitoringWebDAVBackupConfig   = proobservability.MonitoringWebDAVBackupConfig
	ModelPrice                     = proobservability.ModelPrice
	ModelPriceRate                 = proobservability.ModelPriceRate
	ModelPriceTier                 = proobservability.ModelPriceTier
	ModelPriceRule                 = proobservability.ModelPriceRule
	ModelPriceCostBreakdown        = proobservability.ModelPriceCostBreakdown
	ObservedModel                  = proobservability.ObservedModel
	ModelPriceSyncState            = proobservability.ModelPriceSyncState
	ModelPriceSyncResult           = proobservability.ModelPriceSyncResult
	ModelPriceSyncChange           = proobservability.ModelPriceSyncChange
	ProSetting                     = proobservability.ProSetting
	DataDomainInventory            = proobservability.DataDomainInventory
	DataDomainContributor          = proobservability.DataDomainContributor
	DataDomainContributorFunc      = proobservability.DataDomainContributorFunc
	DataDomainContribution         = proobservability.DataDomainContribution
	DataDomainBackupRecordCounter  = proobservability.DataDomainBackupRecordCounter
	DataDomainBackupRecordImporter = proobservability.DataDomainBackupRecordImporter
	DataDomainBackupImporter       = proobservability.DataDomainBackupImporter
	DataDomainCleanupHandler       = proobservability.DataDomainCleanupHandler
	XAIQuotaObservation            = proobservability.XAIQuotaObservation
)

var (
	LoadConfig                                = proobservability.LoadConfig
	LoadConfigForPath                         = proobservability.LoadConfigForPath
	ResolveDataDirForPath                     = proobservability.ResolveDataDirForPath
	OpenStore                                 = proobservability.OpenStore
	NewServer                                 = proobservability.NewServer
	Start                                     = proobservability.Start
	StartForPath                              = proobservability.StartForPath
	RegisterGinRoutes                         = proobservability.RegisterGinRoutes
	RegisterDataManagementGinRoutes           = proobservability.RegisterDataManagementGinRoutes
	RegisterDataDomainContributor             = proobservability.RegisterDataDomainContributor
	SetDefaultService                         = proobservability.SetDefaultService
	SetAccountInspectionScheduleHandlers      = proobservability.SetAccountInspectionScheduleHandlers
	RegisterAccountInspectionScheduleHandlers = proobservability.RegisterAccountInspectionScheduleHandlers
	SetAccountInspectionSnapshotHandlers      = proobservability.SetAccountInspectionSnapshotHandlers
	RegisterAccountInspectionSnapshotHandlers = proobservability.RegisterAccountInspectionSnapshotHandlers
	SetAuthRuntimeStateImportHandler          = proobservability.SetAuthRuntimeStateImportHandler
	RegisterAuthRuntimeStateImportHandler     = proobservability.RegisterAuthRuntimeStateImportHandler
	SetLegacyQuotaCleanupHandler              = proobservability.SetLegacyQuotaCleanupHandler
	RegisterLegacyQuotaCleanupHandler         = proobservability.RegisterLegacyQuotaCleanupHandler
	RegisterProSettingConsumer                = proobservability.RegisterProSettingConsumer
	ApplyImportedProSettings                  = proobservability.ApplyImportedProSettings
	SetQuotaCache                             = proobservability.SetQuotaCache
	GetQuotaCache                             = proobservability.GetQuotaCache
	GetProSetting                             = proobservability.GetProSetting
	SetProSetting                             = proobservability.SetProSetting
	SetProSettingAndApplyLatest               = proobservability.SetProSettingAndApplyLatest
	WithProSettingWriter                      = proobservability.WithProSettingWriter
	QueueRoutingCursorState                   = proobservability.QueueRoutingCursorState
	GetRoutingCursorState                     = proobservability.GetRoutingCursorState
	ListRoutingCursorStates                   = proobservability.ListRoutingCursorStates
	QueueAuthRuntimeStats                     = proobservability.QueueAuthRuntimeStats
	GetAuthRuntimeStats                       = proobservability.GetAuthRuntimeStats
	DeleteQuotaCache                          = proobservability.DeleteQuotaCache
	DeleteAuthRuntimeState                    = proobservability.DeleteAuthRuntimeState
	ObserveXAIQuotaResponse                   = proobservability.ObserveXAIQuotaResponse
	MergeXAIQuotaCache                        = proobservability.MergeXAIQuotaCache
	GetXAIQuotaState                          = proobservability.GetXAIQuotaState
)
