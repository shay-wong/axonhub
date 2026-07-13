package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/datastorage"
	"github.com/looplj/axonhub/internal/ent/project"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/schema/schematype"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
)

const usageBackupBatchSize = 500

type usageBackupChannelIdentity struct {
	name      string
	deletedAt int
}

type usageBackupDataStorageIdentity struct {
	name      string
	deletedAt int
}

func (svc *BackupService) Backup(ctx context.Context, opts BackupOptions) ([]byte, error) {
	user, ok := contexts.GetUser(ctx)
	if !ok || user == nil {
		return nil, fmt.Errorf("user not found in context")
	}

	if !user.IsOwner {
		return nil, fmt.Errorf("only owners can perform backup operations")
	}

	return svc.doBackup(ctx, opts)
}

// BackupWithoutAuth performs backup without user authentication check.
// This is used by the auto-backup scheduler which runs in a privileged context.
func (svc *BackupService) BackupWithoutAuth(ctx context.Context, opts BackupOptions) ([]byte, error) {
	return svc.doBackup(ctx, opts)
}

func (svc *BackupService) doBackup(ctx context.Context, opts BackupOptions) ([]byte, error) {
	var (
		projectDataList           []*BackupProject
		channelDataList           []*BackupChannel
		channelModelPriceDataList []*BackupChannelModelPrice
	)

	if opts.IncludeProjects {
		projects, err := svc.db.Project.Query().All(ctx)
		if err != nil {
			return nil, err
		}

		projectDataList = lo.Map(projects, func(proj *ent.Project, _ int) *BackupProject {
			return &BackupProject{Project: *proj}
		})
	}

	if opts.IncludeChannels {
		channels, err := svc.db.Channel.Query().All(ctx)
		if err != nil {
			return nil, err
		}

		channelDataList = lo.Map(channels, func(ch *ent.Channel, _ int) *BackupChannel {
			return &BackupChannel{
				Channel:     *ch,
				Credentials: ch.Credentials,
			}
		})
	}

	if opts.IncludeModelPrices {
		prices, err := svc.db.ChannelModelPrice.Query().
			WithChannel().
			All(ctx)
		if err != nil {
			return nil, err
		}

		channelModelPriceDataList = lo.FilterMap(prices, func(p *ent.ChannelModelPrice, _ int) (*BackupChannelModelPrice, bool) {
			if p.Edges.Channel == nil {
				return nil, false
			}

			return &BackupChannelModelPrice{
				ChannelName: p.Edges.Channel.Name,
				ModelID:     p.ModelID,
				Price:       p.Price,
				ReferenceID: p.ReferenceID,
			}, true
		})
	}

	var modelDataList []*BackupModel

	if opts.IncludeModels {
		models, err := svc.db.Model.Query().All(ctx)
		if err != nil {
			return nil, err
		}

		modelDataList = lo.Map(models, func(m *ent.Model, _ int) *BackupModel {
			return &BackupModel{
				Model: *m,
			}
		})
	}

	var apiKeyDataList []*BackupAPIKey

	if opts.IncludeAPIKeys {
		apiKeys, err := svc.db.APIKey.Query().WithProject().All(ctx)
		if err != nil {
			return nil, err
		}

		apiKeyDataList = lo.Map(apiKeys, func(ak *ent.APIKey, _ int) *BackupAPIKey {
			projectName := ""
			if ak.Edges.Project != nil {
				projectName = ak.Edges.Project.Name
			}

			return &BackupAPIKey{
				APIKey:      *ak,
				ProjectName: projectName,
			}
		})
	}

	var (
		usageRequestDataList     []*BackupUsageRequest
		requestExecutionDataList []*BackupRequestExecution
		usageLogDataList         []*BackupUsageLog
		usageChannels            map[int]usageBackupChannelIdentity
		usageDataStorages        map[int]*ent.DataStorage
	)

	if opts.IncludeRequestLogs || opts.IncludeUsageStats {
		var err error
		usageChannels, err = svc.backupUsageChannels(ctx)
		if err != nil {
			return nil, err
		}
		usageDataStorages, err = svc.backupDataStorages(ctx, opts.IncludeRequestLogs)
		if err != nil {
			return nil, err
		}
	}

	if opts.IncludeRequestLogs {
		var err error
		usageRequestDataList, err = svc.backupUsageRequests(ctx, opts.IncludeAPIKeys, usageChannels, usageDataStorages)
		if err != nil {
			return nil, err
		}
	}

	if opts.IncludeUsageStats {
		var err error
		usageLogDataList, err = svc.backupUsageLogs(ctx, opts.IncludeAPIKeys, opts.IncludeRequestLogs, usageChannels)
		if err != nil {
			return nil, err
		}
	}

	if opts.IncludeRequestLogs || opts.IncludeUsageStats {
		executionIDs := make([]int, 0, len(usageLogDataList))
		if !opts.IncludeRequestLogs {
			for _, usageLog := range usageLogDataList {
				if usageLog != nil && usageLog.RequestExecutionID != 0 {
					executionIDs = append(executionIDs, usageLog.RequestExecutionID)
				}
			}
		}

		var err error
		requestExecutionDataList, err = svc.backupRequestExecutions(ctx, usageChannels, usageDataStorages, opts.IncludeRequestLogs, executionIDs)
		if err != nil {
			return nil, err
		}
	}

	backupData := &BackupData{
		Version:            BackupVersion,
		Timestamp:          time.Now(),
		Projects:           projectDataList,
		Channels:           channelDataList,
		Models:             modelDataList,
		ChannelModelPrices: channelModelPriceDataList,
		APIKeys:            apiKeyDataList,
		UsageRequests:      usageRequestDataList,
		RequestExecutions:  requestExecutionDataList,
		UsageLogs:          usageLogDataList,
	}

	if opts.IncludeUsageStats || opts.IncludeRequestLogs {
		return json.Marshal(backupData)
	}

	return json.MarshalIndent(backupData, "", "  ")
}

func (svc *BackupService) backupUsageChannels(ctx context.Context) (map[int]usageBackupChannelIdentity, error) {
	channels, err := svc.db.Channel.Query().
		Select(channel.FieldID, channel.FieldName, channel.FieldDeletedAt).
		All(schematype.SkipSoftDelete(ctx))
	if err != nil {
		return nil, err
	}

	identities := make(map[int]usageBackupChannelIdentity, len(channels))
	for _, ch := range channels {
		identities[ch.ID] = usageBackupChannelIdentity{
			name:      ch.Name,
			deletedAt: ch.DeletedAt,
		}
	}

	return identities, nil
}

func (svc *BackupService) backupUsageRequests(
	ctx context.Context,
	includeAPIKeyValues bool,
	channels map[int]usageBackupChannelIdentity,
	dataStorages map[int]*ent.DataStorage,
) ([]*BackupUsageRequest, error) {
	var usageRequestDataList []*BackupUsageRequest
	lastID := 0

	for {
		query := svc.db.Request.Query().
			Where(request.IDGT(lastID)).
			Order(ent.Asc(request.FieldID)).
			Limit(usageBackupBatchSize).
			WithProject()
		if includeAPIKeyValues {
			query.WithAPIKey()
		}

		usageRequests, err := query.All(ctx)
		if err != nil {
			return nil, err
		}

		if len(usageRequests) == 0 {
			break
		}

		for _, req := range usageRequests {
			if err := svc.hydrateBackupUsageRequest(ctx, req, dataStorages[req.DataStorageID]); err != nil {
				return nil, err
			}
			usageRequestDataList = append(usageRequestDataList, backupUsageRequest(req, includeAPIKeyValues, channels))
			lastID = req.ID
		}

		if len(usageRequests) < usageBackupBatchSize {
			break
		}
	}

	return usageRequestDataList, nil
}

func backupUsageRequest(req *ent.Request, includeAPIKeyValues bool, channels map[int]usageBackupChannelIdentity) *BackupUsageRequest {
	data := &BackupUsageRequest{Request: *req}
	// Binary artifacts remain in their external storage and cannot be restored under
	// a new request ID. Keep request-log backups self-contained and non-misleading.
	data.ContentSaved = false
	data.ContentStorageID = nil
	data.ContentStorageKey = nil
	data.ContentSavedAt = nil
	if req.Edges.Project != nil {
		data.ProjectName = req.Edges.Project.Name
	}
	channelIdentity := channels[req.ChannelID]
	data.ChannelName = channelIdentity.name
	data.ChannelDeletedAt = channelIdentity.deletedAt
	if includeAPIKeyValues && req.Edges.APIKey != nil {
		data.APIKeyKey = req.Edges.APIKey.Key
	}
	data.Request.Edges = ent.RequestEdges{}

	return data
}

func (svc *BackupService) backupRequestExecutions(
	ctx context.Context,
	channels map[int]usageBackupChannelIdentity,
	dataStorages map[int]*ent.DataStorage,
	includeAll bool,
	executionIDs []int,
) ([]*BackupRequestExecution, error) {
	if !includeAll && len(executionIDs) == 0 {
		return nil, nil
	}

	projectNames, err := svc.backupProjectNames(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]*BackupRequestExecution, 0)
	appendExecutions := func(executions []*ent.RequestExecution) error {
		for _, execution := range executions {
			if includeAll {
				if err := svc.hydrateBackupRequestExecution(ctx, execution, dataStorages[execution.DataStorageID]); err != nil {
					return err
				}
			}
			result = append(result, backupRequestExecution(execution, projectNames, channels, dataStorages, includeAll))
		}
		return nil
	}

	if includeAll {
		lastID := 0
		for {
			executions, err := svc.db.RequestExecution.Query().
				Where(requestexecution.IDGT(lastID)).
				Order(ent.Asc(requestexecution.FieldID)).
				Limit(usageBackupBatchSize).
				All(ctx)
			if err != nil {
				return nil, err
			}
			if len(executions) == 0 {
				break
			}

			if err := appendExecutions(executions); err != nil {
				return nil, err
			}
			lastID = executions[len(executions)-1].ID
			if len(executions) < usageBackupBatchSize {
				break
			}
		}

		return result, nil
	}

	executionIDs = lo.Uniq(executionIDs)
	for start := 0; start < len(executionIDs); start += usageBackupBatchSize {
		end := min(start+usageBackupBatchSize, len(executionIDs))
		executions, err := svc.db.RequestExecution.Query().
			Where(requestexecution.IDIn(executionIDs[start:end]...)).
			Order(ent.Asc(requestexecution.FieldID)).
			Select(
				requestexecution.FieldID,
				requestexecution.FieldCreatedAt,
				requestexecution.FieldUpdatedAt,
				requestexecution.FieldProjectID,
				requestexecution.FieldRequestID,
				requestexecution.FieldChannelID,
				requestexecution.FieldDataStorageID,
				requestexecution.FieldSource,
				requestexecution.FieldModelID,
				requestexecution.FieldFormat,
				requestexecution.FieldRequestedServiceTier,
				requestexecution.FieldSpeedMode,
				requestexecution.FieldChannelAPIKeyName,
				requestexecution.FieldChannelAPIKeySuffix,
				requestexecution.FieldChannelAPIKeyHeaders,
				requestexecution.FieldResponseStatusCode,
				requestexecution.FieldStatus,
				requestexecution.FieldStream,
				requestexecution.FieldMetricsLatencyMs,
				requestexecution.FieldMetricsFirstTokenLatencyMs,
				requestexecution.FieldMetricsReasoningDurationMs,
				requestexecution.FieldPassThroughApplied,
			).
			All(ctx)
		if err != nil {
			return nil, err
		}
		if err := appendExecutions(executions); err != nil {
			return nil, err
		}
	}

	return result, nil
}

func (svc *BackupService) backupProjectNames(ctx context.Context) (map[int]string, error) {
	projects, err := svc.db.Project.Query().
		Select(project.FieldID, project.FieldName).
		All(schematype.SkipSoftDelete(ctx))
	if err != nil {
		return nil, err
	}

	names := make(map[int]string, len(projects))
	for _, proj := range projects {
		names[proj.ID] = proj.Name
	}

	return names, nil
}

func (svc *BackupService) backupDataStorages(ctx context.Context, includeContent bool) (map[int]*ent.DataStorage, error) {
	query := svc.db.DataStorage.Query()
	if !includeContent {
		query.Select(datastorage.FieldID, datastorage.FieldName, datastorage.FieldDeletedAt)
	}
	dataStorages, err := query.All(schematype.SkipSoftDelete(ctx))
	if err != nil {
		return nil, err
	}

	storages := make(map[int]*ent.DataStorage, len(dataStorages))
	for _, storage := range dataStorages {
		storages[storage.ID] = storage
	}

	return storages, nil
}

func backupRequestExecution(
	execution *ent.RequestExecution,
	projectNames map[int]string,
	channels map[int]usageBackupChannelIdentity,
	dataStorages map[int]*ent.DataStorage,
	includeContent bool,
) *BackupRequestExecution {
	channelIdentity := channels[execution.ChannelID]
	dataStorage := dataStorages[execution.DataStorageID]
	dataStorageIdentity := usageBackupDataStorageIdentity{}
	if dataStorage != nil {
		dataStorageIdentity = usageBackupDataStorageIdentity{
			name:      dataStorage.Name,
			deletedAt: dataStorage.DeletedAt,
		}
	}

	data := &BackupRequestExecution{
		ID:                         execution.ID,
		CreatedAt:                  execution.CreatedAt,
		UpdatedAt:                  execution.UpdatedAt,
		ProjectID:                  execution.ProjectID,
		RequestID:                  execution.RequestID,
		ChannelID:                  execution.ChannelID,
		DataStorageID:              execution.DataStorageID,
		Source:                     execution.Source,
		ModelID:                    execution.ModelID,
		Format:                     execution.Format,
		RequestedServiceTier:       llm.CanonicalServiceTier(execution.RequestedServiceTier),
		SpeedMode:                  strings.ToLower(strings.TrimSpace(execution.SpeedMode)),
		ChannelAPIKeyName:          execution.ChannelAPIKeyName,
		ChannelAPIKeySuffix:        execution.ChannelAPIKeySuffix,
		ChannelAPIKeyHeaders:       execution.ChannelAPIKeyHeaders,
		RequestBody:                objects.JSONRawMessage("{}"),
		ResponseStatusCode:         execution.ResponseStatusCode,
		Status:                     execution.Status,
		Stream:                     execution.Stream,
		MetricsLatencyMs:           execution.MetricsLatencyMs,
		MetricsFirstTokenLatencyMs: execution.MetricsFirstTokenLatencyMs,
		MetricsReasoningDurationMs: execution.MetricsReasoningDurationMs,
		PassThroughApplied:         execution.PassThroughApplied,
		ProjectName:                projectNames[execution.ProjectID],
		ChannelName:                channelIdentity.name,
		ChannelDeletedAt:           channelIdentity.deletedAt,
		DataStorageName:            dataStorageIdentity.name,
		DataStorageDeletedAt:       dataStorageIdentity.deletedAt,
	}
	if includeContent {
		data.ExternalID = execution.ExternalID
		data.RequestBody = execution.RequestBody
		data.ResponseBody = execution.ResponseBody
		data.ResponseChunks = execution.ResponseChunks
		data.ErrorMessage = execution.ErrorMessage
		data.RequestHeaders = execution.RequestHeaders
		data.RequestURL = execution.RequestURL
	}

	return data
}

func (svc *BackupService) hydrateBackupUsageRequest(ctx context.Context, req *ent.Request, dataStorage *ent.DataStorage) error {
	if req == nil {
		return nil
	}

	var err error
	if req.RequestBody, err = svc.loadBackupJSON(ctx, dataStorage, biz.GenerateRequestBodyKey(req.ProjectID, req.ID), req.RequestBody); err != nil {
		return fmt.Errorf("hydrate request body %d: %w", req.ID, err)
	}
	if req.ResponseBody, err = svc.loadBackupJSON(ctx, dataStorage, biz.GenerateResponseBodyKey(req.ProjectID, req.ID), req.ResponseBody); err != nil {
		return fmt.Errorf("hydrate request response body %d: %w", req.ID, err)
	}
	if req.ResponseChunks, err = svc.loadBackupChunks(ctx, dataStorage, biz.GenerateResponseChunksKey(req.ProjectID, req.ID), req.ResponseChunks); err != nil {
		return fmt.Errorf("hydrate request response chunks %d: %w", req.ID, err)
	}

	return nil
}

func (svc *BackupService) hydrateBackupRequestExecution(ctx context.Context, execution *ent.RequestExecution, dataStorage *ent.DataStorage) error {
	if execution == nil {
		return nil
	}

	var err error
	if execution.RequestBody, err = svc.loadBackupJSON(ctx, dataStorage, biz.GenerateExecutionRequestBodyKey(execution.ProjectID, execution.RequestID, execution.ID), execution.RequestBody); err != nil {
		return fmt.Errorf("hydrate request execution body %d: %w", execution.ID, err)
	}
	if execution.ResponseBody, err = svc.loadBackupJSON(ctx, dataStorage, biz.GenerateExecutionResponseBodyKey(execution.ProjectID, execution.RequestID, execution.ID), execution.ResponseBody); err != nil {
		return fmt.Errorf("hydrate request execution response body %d: %w", execution.ID, err)
	}
	if execution.ResponseChunks, err = svc.loadBackupChunks(ctx, dataStorage, biz.GenerateExecutionResponseChunksKey(execution.ProjectID, execution.RequestID, execution.ID), execution.ResponseChunks); err != nil {
		return fmt.Errorf("hydrate request execution response chunks %d: %w", execution.ID, err)
	}

	return nil
}

func (svc *BackupService) loadBackupJSON(
	ctx context.Context,
	dataStorage *ent.DataStorage,
	key string,
	fallback objects.JSONRawMessage,
) (objects.JSONRawMessage, error) {
	data, ok, err := svc.loadBackupData(ctx, dataStorage, key)
	if err != nil {
		return nil, err
	}
	if !ok {
		return fallback, nil
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("external content is not valid JSON")
	}

	return objects.JSONRawMessage(data), nil
}

func (svc *BackupService) loadBackupChunks(
	ctx context.Context,
	dataStorage *ent.DataStorage,
	key string,
	fallback []objects.JSONRawMessage,
) ([]objects.JSONRawMessage, error) {
	data, ok, err := svc.loadBackupData(ctx, dataStorage, key)
	if err != nil {
		return nil, err
	}
	if !ok {
		return fallback, nil
	}

	var chunks []objects.JSONRawMessage
	if err := json.Unmarshal(data, &chunks); err != nil {
		return nil, fmt.Errorf("external content is not a JSON chunk array: %w", err)
	}

	return chunks, nil
}

func (svc *BackupService) loadBackupData(ctx context.Context, dataStorage *ent.DataStorage, key string) ([]byte, bool, error) {
	if dataStorage == nil || dataStorage.Primary || dataStorage.Type == datastorage.TypeDatabase {
		return nil, false, nil
	}
	if svc.dataStorageService == nil {
		return nil, false, fmt.Errorf("data storage service is unavailable")
	}

	data, err := svc.dataStorageService.LoadData(ctx, dataStorage, key)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	return data, true, nil
}

func (svc *BackupService) backupUsageLogs(
	ctx context.Context,
	includeAPIKeyValues bool,
	includeRequestDetails bool,
	channels map[int]usageBackupChannelIdentity,
) ([]*BackupUsageLog, error) {
	var usageLogDataList []*BackupUsageLog
	apiKeyKeys := map[int]string{}
	lastID := 0

	if includeAPIKeyValues {
		apiKeys, err := svc.db.APIKey.Query().
			Select(apikey.FieldID, apikey.FieldKey).
			All(ctx)
		if err != nil {
			return nil, err
		}

		for _, ak := range apiKeys {
			apiKeyKeys[ak.ID] = ak.Key
		}
	}

	for {
		query := svc.db.UsageLog.Query().
			Where(usagelog.IDGT(lastID)).
			Order(ent.Asc(usagelog.FieldID)).
			Limit(usageBackupBatchSize).
			WithProject().
			WithRequest(func(query *ent.RequestQuery) {
				query.Select(
					request.FieldID,
					request.FieldCreatedAt,
					request.FieldSource,
					request.FieldModelID,
					request.FieldReasoningEffort,
					request.FieldFormat,
					request.FieldStream,
				)
			}).
			WithRequestExecution(func(query *ent.RequestExecutionQuery) {
				fields := []string{
					requestexecution.FieldID,
					requestexecution.FieldCreatedAt,
					requestexecution.FieldFormat,
				}
				if includeRequestDetails {
					fields = append(fields, requestexecution.FieldRequestURL)
				}
				query.Select(fields...)
			})

		usageLogs, err := query.All(ctx)
		if err != nil {
			return nil, err
		}

		if len(usageLogs) == 0 {
			break
		}

		for _, ul := range usageLogs {
			usageLogDataList = append(usageLogDataList, backupUsageLog(ul, apiKeyKeys, channels, includeRequestDetails))
			lastID = ul.ID
		}

		if len(usageLogs) < usageBackupBatchSize {
			break
		}
	}

	return usageLogDataList, nil
}

func backupUsageLog(
	ul *ent.UsageLog,
	apiKeyKeys map[int]string,
	channels map[int]usageBackupChannelIdentity,
	includeRequestDetails bool,
) *BackupUsageLog {
	data := &BackupUsageLog{UsageLog: *ul}
	data.RequestedServiceTier = llm.CanonicalServiceTier(data.RequestedServiceTier)
	data.AppliedServiceTier = llm.CanonicalServiceTier(data.AppliedServiceTier)
	data.ServiceTier = llm.CanonicalServiceTier(data.ServiceTier)
	if ul.Edges.Project != nil {
		data.ProjectName = ul.Edges.Project.Name
	}
	channelIdentity := channels[ul.ChannelID]
	data.ChannelName = channelIdentity.name
	data.ChannelDeletedAt = channelIdentity.deletedAt
	if ul.APIKeyID != 0 {
		data.APIKeyKey = apiKeyKeys[ul.APIKeyID]
	}
	if ul.Edges.Request != nil {
		req := ul.Edges.Request
		data.RequestCreatedAt = req.CreatedAt
		data.RequestSource = req.Source
		data.RequestModelID = req.ModelID
		data.RequestReasoningEffort = req.ReasoningEffort
		data.RequestFormat = req.Format
		data.RequestStream = req.Stream
	}
	if ul.Edges.RequestExecution != nil {
		data.RequestExecutionCreatedAt = ul.Edges.RequestExecution.CreatedAt
		data.RequestExecutionFormat = ul.Edges.RequestExecution.Format
		if includeRequestDetails {
			data.RequestExecutionRequestURL = ul.Edges.RequestExecution.RequestURL
		}
	}
	data.UsageLog.Edges = ent.UsageLogEdges{}

	return data
}
