// 职责: 验证分块请求正文在备份、恢复和重复恢复中的完整性。
// 关联文件:
//
//	internal/server/backup/backup_ops.go
//	internal/server/backup/restore.go
//	internal/server/biz/request_body_chunks.go
//
// 注意事项: 备份必须展开正文，内部块引用不能进入可移植备份格式。
package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/server/biz"
)

func chunkedBackupFixture(t *testing.T) (*ent.Client, *BackupService, context.Context, *ent.Project, *ent.Request, *ent.RequestExecution, []byte) {
	t.Helper()
	client, service, ctx := setupBackupTestWithDSN(t, "file:"+strings.ReplaceAll(t.Name(), "/", "-")+"-source?mode=memory&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	user := client.User.Query().OnlyX(ctx)
	project := createBackupTestProject(t, client, ctx, "Chunked Project", "source")
	ch := createBackupTestChannel(t, client, ctx, "Chunked Channel", channel.TypeOpenai)
	key := createBackupTestAPIKey(t, client, ctx, user, project, "chunked key", "sk-chunked-backup-test")
	body := []byte(fmt.Sprintf(" { \"input\": [ { \"type\": \"function_call_output\", \"namespace\": \"agent\", \"call_id\": \"call_1\", \"output\": %q } ], \"counter\": 9007199254740993 } ", strings.Repeat("tool output to preserve ", 6000)))
	plan := biz.PrepareChunkedRequestBody(body)
	require.NotNil(t, plan)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	db := tx.Client()
	req, err := db.Request.Create().SetProjectID(project.ID).SetAPIKeyID(key.ID).SetChannelID(ch.ID).
		SetModelID("gpt-6.1-sol").SetFormat("openai/responses").SetRequestBody(plan.Manifest).
		SetRequestBodyChunked(true).SetStatus(request.StatusCompleted).SetStream(true).Save(ctx)
	require.NoError(t, err)
	require.NoError(t, plan.Save(ctx, db, project.ID, req.ID, 0))
	execution, err := db.RequestExecution.Create().SetProjectID(project.ID).SetRequestID(req.ID).SetChannelID(ch.ID).
		SetModelID("gpt-6.1-sol").SetFormat("openai/responses").SetRequestBody(plan.Manifest).
		SetRequestBodyChunked(true).SetStatus(requestexecution.StatusCompleted).SetStream(true).Save(ctx)
	require.NoError(t, err)
	require.NoError(t, plan.Save(ctx, db, project.ID, 0, execution.ID))
	_, err = db.UsageLog.Create().SetRequestID(req.ID).SetRequestExecutionID(execution.ID).SetAPIKeyID(key.ID).
		SetProjectID(project.ID).SetChannelID(ch.ID).SetModelID("gpt-6.1-sol").SetPromptTokens(100).
		SetCompletionTokens(20).SetTotalTokens(120).SetSource("api").SetFormat("openai/responses").Save(ctx)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	return client, service, ctx, project, req, execution, body
}

func TestBackupChunkedRequestBodiesRoundTrip(t *testing.T) {
	source, service, ctx, project, _, _, body := chunkedBackupFixture(t)
	data, err := service.Backup(ctx, BackupOptions{IncludeAPIKeys: true, IncludeUsageStats: true, IncludeRequestLogs: true})
	require.NoError(t, err)
	require.NotContains(t, string(data), "request_body_chunked")
	var backup BackupData
	require.NoError(t, json.Unmarshal(data, &backup))
	require.Len(t, backup.UsageRequests, 1)
	require.Len(t, backup.RequestExecutions, 1)
	var compact bytes.Buffer
	require.NoError(t, json.Compact(&compact, body))
	require.Equal(t, compact.String(), string(backup.UsageRequests[0].RequestBody))
	require.Equal(t, compact.String(), string(backup.RequestExecutions[0].RequestBody))

	// Restoring into the source must compare expanded content, not its manifest.
	opts := RestoreOptions{IncludeAPIKeys: true, IncludeRequestLogs: true, IncludeUsageStats: true}
	require.NoError(t, service.Restore(ctx, bytes.NewReader(data), opts))
	require.Equal(t, 1, source.Request.Query().CountX(ctx))
	require.Equal(t, 1, source.RequestExecution.Query().CountX(ctx))

	target, targetService, targetCtx := setupBackupTestWithDSN(t, "file:chunked-backup-target?mode=memory&_fk=1")
	defer target.Close()
	createBackupTestProject(t, target, targetCtx, "Unrelated", "changes target IDs")
	targetProject := createBackupTestProject(t, target, targetCtx, project.Name, "target")
	require.NotEqual(t, project.ID, targetProject.ID)
	createBackupTestChannel(t, target, targetCtx, "Chunked Channel", channel.TypeOpenai)
	require.NoError(t, targetService.Restore(targetCtx, bytes.NewReader(data), opts))
	restoredRequest := target.Request.Query().OnlyX(targetCtx)
	restoredExecution := target.RequestExecution.Query().OnlyX(targetCtx)
	require.Equal(t, targetProject.ID, restoredRequest.ProjectID)
	require.Equal(t, targetProject.ID, restoredExecution.ProjectID)
	require.True(t, restoredRequest.RequestBodyChunked)
	require.True(t, restoredExecution.RequestBodyChunked)
	requestBody, err := biz.LoadChunkedRequestBody(targetCtx, target, targetProject.ID, restoredRequest.ID, 0, restoredRequest.RequestBody)
	require.NoError(t, err)
	executionBody, err := biz.LoadChunkedRequestBody(targetCtx, target, targetProject.ID, 0, restoredExecution.ID, restoredExecution.RequestBody)
	require.NoError(t, err)
	require.Equal(t, compact.Bytes(), []byte(requestBody))
	require.Equal(t, compact.Bytes(), []byte(executionBody))
	chunks := target.RequestBodyChunk.Query().CountX(targetCtx)
	refs := target.RequestBodyChunkRef.Query().CountX(targetCtx)
	require.NoError(t, targetService.Restore(targetCtx, bytes.NewReader(data), opts))
	require.Equal(t, 1, target.Request.Query().CountX(targetCtx))
	require.Equal(t, 1, target.RequestExecution.Query().CountX(targetCtx))
	require.Equal(t, chunks, target.RequestBodyChunk.Query().CountX(targetCtx))
	require.Equal(t, refs, target.RequestBodyChunkRef.Query().CountX(targetCtx))
}

func TestBackupChunkedRequestBodiesStatsOnlySkipsBrokenReferences(t *testing.T) {
	client, service, ctx, _, _, _, _ := chunkedBackupFixture(t)
	full, err := service.Backup(ctx, BackupOptions{IncludeAPIKeys: true, IncludeUsageStats: true, IncludeRequestLogs: true})
	require.NoError(t, err)
	_, err = client.RequestBodyChunkRef.Delete().Exec(ctx)
	require.NoError(t, err)
	_, err = service.Backup(ctx, BackupOptions{IncludeRequestLogs: true})
	require.ErrorContains(t, err, "hydrate request body")
	data, err := service.Backup(ctx, BackupOptions{IncludeUsageStats: true})
	require.NoError(t, err)
	var backup BackupData
	require.NoError(t, json.Unmarshal(data, &backup))
	require.Empty(t, backup.UsageRequests)
	require.Len(t, backup.RequestExecutions, 1)
	require.JSONEq(t, "{}", string(backup.RequestExecutions[0].RequestBody))

	target, targetService, targetCtx := setupBackupTestWithDSN(t, "file:chunked-backup-stats-target?mode=memory&_fk=1")
	defer target.Close()
	createBackupTestProject(t, target, targetCtx, "Chunked Project", "target")
	createBackupTestChannel(t, target, targetCtx, "Chunked Channel", channel.TypeOpenai)
	require.NoError(t, targetService.Restore(targetCtx, bytes.NewReader(full), RestoreOptions{IncludeAPIKeys: true, IncludeUsageStats: true}))
	require.Equal(t, 1, target.RequestExecution.Query().CountX(targetCtx))
	require.False(t, target.RequestExecution.Query().OnlyX(targetCtx).RequestBodyChunked)
	require.Zero(t, target.RequestBodyChunk.Query().CountX(targetCtx))
	require.Zero(t, target.RequestBodyChunkRef.Query().CountX(targetCtx))
}

func TestRestoreChunkedRequestBodiesRollsBackWithOwner(t *testing.T) {
	_, service, ctx, _, _, _, _ := chunkedBackupFixture(t)
	data, err := service.Backup(ctx, BackupOptions{IncludeRequestLogs: true})
	require.NoError(t, err)
	target, targetService, targetCtx := setupBackupTestWithDSN(t, "file:chunked-backup-rollback-target?mode=memory&_fk=1")
	defer target.Close()
	createBackupTestProject(t, target, targetCtx, "Chunked Project", "target")
	createBackupTestChannel(t, target, targetCtx, "Chunked Channel", channel.TypeOpenai)
	target.RequestExecution.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			return nil, errors.New("injected execution restore failure")
		})
	})
	err = targetService.Restore(targetCtx, bytes.NewReader(data), RestoreOptions{IncludeRequestLogs: true})
	require.ErrorContains(t, err, "injected execution restore failure")
	require.Zero(t, target.Request.Query().CountX(targetCtx))
	require.Zero(t, target.RequestExecution.Query().CountX(targetCtx))
	require.Zero(t, target.RequestBodyChunk.Query().CountX(targetCtx))
	require.Zero(t, target.RequestBodyChunkRef.Query().CountX(targetCtx))
}

func TestRequestExecutionJSONFingerprintPreservesNumbers(t *testing.T) {
	left := []byte(`{ "counter": 9007199254740993, "input": [1, 2] }`)
	reordered := []byte(`{"input":[1,2],"counter":9007199254740993}`)
	differentNumber := []byte(`{"input":[1,2],"counter":9007199254740992}`)
	require.Equal(t, requestExecutionJSONFingerprint(left), requestExecutionJSONFingerprint(reordered))
	require.NotEqual(t, requestExecutionJSONFingerprint(left), requestExecutionJSONFingerprint(differentNumber))
}
