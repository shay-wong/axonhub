package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/channelmodelprice"
	"github.com/looplj/axonhub/internal/ent/datastorage"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/ent/project"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/schema/schematype"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
)

const (
	backupBatchSize      = 500
	usageBackupBatchSize = backupBatchSize
)

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
	var buf bytes.Buffer
	if err := svc.doBackupToWriter(ctx, opts, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// doBackupToWriter streams a compact JSON backup to w without accumulating
// the full dataset in memory. Each entity type is processed sequentially in
// batch-sized pages using an ID-cursor.
func (svc *BackupService) doBackupToWriter(ctx context.Context, opts BackupOptions, w io.Writer) error {
	o := &objWriter{w: w}

	if _, err := w.Write([]byte("{")); err != nil {
		return err
	}

	if b, err := json.Marshal(BackupVersion); err != nil {
		return err
	} else if err := o.rawField("version", b); err != nil {
		return err
	}

	if b, err := json.Marshal(time.Now()); err != nil {
		return err
	} else if err := o.rawField("timestamp", b); err != nil {
		return err
	}

	if err := svc.streamProjects(ctx, o, opts); err != nil {
		return err
	}
	if err := svc.streamChannels(ctx, o, opts); err != nil {
		return err
	}
	if err := svc.streamModels(ctx, o, opts); err != nil {
		return err
	}
	if err := svc.streamChannelModelPrices(ctx, o, opts); err != nil {
		return err
	}
	if err := svc.streamAPIKeys(ctx, o, opts); err != nil {
		return err
	}
	if err := svc.streamUsageRequests(ctx, o, opts); err != nil {
		return err
	}
	if err := svc.streamRequestExecutions(ctx, o, opts); err != nil {
		return err
	}
	if err := svc.streamUsageLogs(ctx, o, opts); err != nil {
		return err
	}

	_, err := w.Write([]byte("}"))
	return err
}

func (svc *BackupService) streamProjects(ctx context.Context, o *objWriter, opts BackupOptions) error {
	return streamArrayField(o, "projects", opts.IncludeProjects, true,
		func(lastID int) ([]*ent.Project, int, error) {
			rows, err := svc.db.Project.Query().
				Where(project.IDGT(lastID)).
				Order(ent.Asc(project.FieldID)).
				Limit(backupBatchSize).
				All(ctx)
			if err != nil {
				return nil, 0, err
			}
			nextID := 0
			if len(rows) > 0 {
				nextID = rows[len(rows)-1].ID
			}
			return rows, nextID, nil
		},
		func(p *ent.Project) ([]byte, bool, error) {
			b, err := json.Marshal(&BackupProject{Project: *p})
			return b, true, err
		},
	)
}

func (svc *BackupService) streamChannels(ctx context.Context, o *objWriter, opts BackupOptions) error {
	return streamArrayField(o, "channels", opts.IncludeChannels, false,
		func(lastID int) ([]*ent.Channel, int, error) {
			rows, err := svc.db.Channel.Query().
				Where(channel.IDGT(lastID)).
				Order(ent.Asc(channel.FieldID)).
				Limit(backupBatchSize).
				All(ctx)
			if err != nil {
				return nil, 0, err
			}
			nextID := 0
			if len(rows) > 0 {
				nextID = rows[len(rows)-1].ID
			}
			return rows, nextID, nil
		},
		func(ch *ent.Channel) ([]byte, bool, error) {
			b, err := json.Marshal(&BackupChannel{
				Channel:     *ch,
				Credentials: ch.Credentials,
			})
			return b, true, err
		},
	)
}

func (svc *BackupService) streamModels(ctx context.Context, o *objWriter, opts BackupOptions) error {
	return streamArrayField(o, "models", opts.IncludeModels, false,
		func(lastID int) ([]*ent.Model, int, error) {
			rows, err := svc.db.Model.Query().
				Where(model.IDGT(lastID)).
				Order(ent.Asc(model.FieldID)).
				Limit(backupBatchSize).
				All(ctx)
			if err != nil {
				return nil, 0, err
			}
			nextID := 0
			if len(rows) > 0 {
				nextID = rows[len(rows)-1].ID
			}
			return rows, nextID, nil
		},
		func(m *ent.Model) ([]byte, bool, error) {
			b, err := json.Marshal(&BackupModel{Model: *m})
			return b, true, err
		},
	)
}

func (svc *BackupService) streamChannelModelPrices(ctx context.Context, o *objWriter, opts BackupOptions) error {
	return streamArrayField(o, "channel_model_prices", opts.IncludeModelPrices, true,
		func(lastID int) ([]*ent.ChannelModelPrice, int, error) {
			rows, err := svc.db.ChannelModelPrice.Query().
				WithChannel().
				Where(channelmodelprice.IDGT(lastID)).
				Order(ent.Asc(channelmodelprice.FieldID)).
				Limit(backupBatchSize).
				All(ctx)
			if err != nil {
				return nil, 0, err
			}
			nextID := 0
			if len(rows) > 0 {
				nextID = rows[len(rows)-1].ID
			}
			return rows, nextID, nil
		},
		func(p *ent.ChannelModelPrice) ([]byte, bool, error) {
			if p.Edges.Channel == nil {
				return nil, false, nil
			}
			b, err := json.Marshal(&BackupChannelModelPrice{
				ChannelName: p.Edges.Channel.Name,
				ModelID:     p.ModelID,
				Price:       p.Price,
				ReferenceID: p.ReferenceID,
			})
			return b, true, err
		},
	)
}

func (svc *BackupService) streamAPIKeys(ctx context.Context, o *objWriter, opts BackupOptions) error {
	return streamArrayField(o, "api_keys", opts.IncludeAPIKeys, true,
		func(lastID int) ([]*ent.APIKey, int, error) {
			rows, err := svc.db.APIKey.Query().
				WithProject().
				Where(apikey.IDGT(lastID)).
				Order(ent.Asc(apikey.FieldID)).
				Limit(backupBatchSize).
				All(ctx)
			if err != nil {
				return nil, 0, err
			}
			nextID := 0
			if len(rows) > 0 {
				nextID = rows[len(rows)-1].ID
			}
			return rows, nextID, nil
		},
		func(ak *ent.APIKey) ([]byte, bool, error) {
			projectName := ""
			if ak.Edges.Project != nil {
				projectName = ak.Edges.Project.Name
			}
			b, err := json.Marshal(&BackupAPIKey{
				APIKey:      *ak,
				ProjectName: projectName,
			})
			return b, true, err
		},
	)
}

func (svc *BackupService) streamUsageRequests(ctx context.Context, o *objWriter, opts BackupOptions) error {
	if !opts.IncludeRequestLogs {
		return nil
	}

	channels, err := svc.backupUsageChannels(ctx)
	if err != nil {
		return err
	}
	dataStorages, err := svc.backupDataStorages(ctx, true)
	if err != nil {
		return err
	}

	return streamArrayField(o, "usage_requests", true, true,
		func(lastID int) ([]*ent.Request, int, error) {
			query := svc.db.Request.Query().
				Where(request.IDGT(lastID)).
				Order(ent.Asc(request.FieldID)).
				Limit(backupBatchSize).
				WithProject()
			if opts.IncludeAPIKeys {
				query.WithAPIKey()
			}
			rows, err := query.All(ctx)
			if err != nil {
				return nil, 0, err
			}
			nextID := 0
			if len(rows) > 0 {
				nextID = rows[len(rows)-1].ID
			}
			return rows, nextID, nil
		},
		func(req *ent.Request) ([]byte, bool, error) {
			if err := svc.hydrateBackupUsageRequest(ctx, req, dataStorages[req.DataStorageID]); err != nil {
				return nil, false, err
			}
			b, err := json.Marshal(backupUsageRequest(req, opts.IncludeAPIKeys, channels))
			return b, true, err
		},
	)
}

func (svc *BackupService) streamRequestExecutions(ctx context.Context, o *objWriter, opts BackupOptions) error {
	include := opts.IncludeRequestLogs || opts.IncludeUsageStats
	if !include {
		return nil
	}

	channels, err := svc.backupUsageChannels(ctx)
	if err != nil {
		return err
	}
	projectNames, err := svc.backupProjectNames(ctx)
	if err != nil {
		return err
	}
	dataStorages, err := svc.backupDataStorages(ctx, opts.IncludeRequestLogs)
	if err != nil {
		return err
	}

	return streamArrayField(o, "request_executions", true, true,
		func(lastID int) ([]*ent.RequestExecution, int, error) {
			query := svc.db.RequestExecution.Query().
				Where(requestexecution.IDGT(lastID)).
				Order(ent.Asc(requestexecution.FieldID)).
				Limit(backupBatchSize)
			if !opts.IncludeRequestLogs {
				query.Where(requestexecution.HasUsageLog()).Select(
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
				)
			}

			rows, err := query.All(ctx)
			if err != nil {
				return nil, 0, err
			}
			nextID := 0
			if len(rows) > 0 {
				nextID = rows[len(rows)-1].ID
			}
			return rows, nextID, nil
		},
		func(execution *ent.RequestExecution) ([]byte, bool, error) {
			if opts.IncludeRequestLogs {
				if err := svc.hydrateBackupRequestExecution(ctx, execution, dataStorages[execution.DataStorageID]); err != nil {
					return nil, false, err
				}
			}
			b, err := json.Marshal(backupRequestExecution(
				execution,
				projectNames,
				channels,
				dataStorages,
				opts.IncludeRequestLogs,
			))
			return b, true, err
		},
	)
}

func (svc *BackupService) streamUsageLogs(ctx context.Context, o *objWriter, opts BackupOptions) error {
	if !opts.IncludeUsageStats {
		return nil
	}

	channels, err := svc.backupUsageChannels(ctx)
	if err != nil {
		return err
	}
	apiKeyKeys := map[int]string{}
	if opts.IncludeAPIKeys {
		apiKeys, err := svc.db.APIKey.Query().
			Select(apikey.FieldID, apikey.FieldKey).
			All(ctx)
		if err != nil {
			return err
		}
		for _, ak := range apiKeys {
			apiKeyKeys[ak.ID] = ak.Key
		}
	}

	return streamArrayField(o, "usage_logs", true, true,
		func(lastID int) ([]*ent.UsageLog, int, error) {
			query := svc.db.UsageLog.Query().
				Where(usagelog.IDGT(lastID)).
				Order(ent.Asc(usagelog.FieldID)).
				Limit(backupBatchSize).
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
					if opts.IncludeRequestLogs {
						fields = append(fields, requestexecution.FieldRequestURL)
					}
					query.Select(fields...)
				})

			rows, err := query.All(ctx)
			if err != nil {
				return nil, 0, err
			}
			nextID := 0
			if len(rows) > 0 {
				nextID = rows[len(rows)-1].ID
			}
			return rows, nextID, nil
		},
		func(ul *ent.UsageLog) ([]byte, bool, error) {
			b, err := json.Marshal(backupUsageLog(ul, apiKeyKeys, channels, opts.IncludeRequestLogs))
			return b, true, err
		},
	)
}

// objWriter writes a JSON object incrementally, tracking leading commas.
type objWriter struct {
	w         io.Writer
	needComma bool
}

func (o *objWriter) rawField(name string, raw []byte) error {
	if o.needComma {
		if _, err := o.w.Write([]byte(",")); err != nil {
			return err
		}
	}
	o.needComma = true
	if _, err := fmt.Fprintf(o.w, "%q:", name); err != nil {
		return err
	}
	_, err := o.w.Write(raw)
	return err
}

// streamArrayField streams a JSON array field incrementally, processing rows
// in pages via fetchBatch and transforming each via elem.
func streamArrayField[T any](
	o *objWriter, name string, on bool, omitempty bool,
	fetchBatch func(lastID int) (rows []T, nextID int, err error),
	elem func(T) (jsonBytes []byte, emit bool, err error),
) error {
	if !on {
		if omitempty {
			return nil
		}
		return o.rawField(name, []byte("null"))
	}
	lastID := 0
	opened := false
	for {
		rows, nextID, err := fetchBatch(lastID)
		if err != nil {
			if opened {
				_, _ = o.w.Write([]byte("]"))
			}
			return err
		}
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			b, emit, err := elem(r)
			if err != nil {
				if opened {
					_, _ = o.w.Write([]byte("]"))
				}
				return err
			}
			if !emit {
				continue
			}
			if !opened {
				if err := o.rawField(name, []byte("[")); err != nil {
					return err
				}
				opened = true
				if _, err := o.w.Write(b); err != nil {
					return err
				}
			} else {
				if _, err := o.w.Write([]byte(",")); err != nil {
					return err
				}
				if _, err := o.w.Write(b); err != nil {
					return err
				}
			}
		}
		lastID = nextID
		if len(rows) < backupBatchSize {
			break
		}
	}
	if opened {
		_, err := o.w.Write([]byte("]"))
		return err
	}
	if omitempty {
		return nil
	}
	return o.rawField(name, []byte("[]"))
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

func backupUsageRequest(req *ent.Request, includeAPIKeyValues bool, channels map[int]usageBackupChannelIdentity) *BackupUsageRequest {
	data := &BackupUsageRequest{Request: *req}
	// Binary artifacts cannot be restored under a new request ID, so do not
	// preserve pointers to content that remains in the original storage.
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
