package v1

import (
	"context"
	"database/sql"
	"errors"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/query"
	annotationsquery "github.com/everstacklabs/everstack/internal/query/handlers/annotations"
	"github.com/lib/pq"
)

func queueAnnotatorAuthorized(annotators []string, userID string) bool {
	if len(annotators) == 0 {
		return true
	}
	if userID == "" {
		return false
	}
	for _, annotator := range annotators {
		if annotator == userID {
			return true
		}
	}
	return false
}

func (s *Server) assertQueueAnnotator(ctx context.Context, queueID, tenantID, userID string) error {
	if tenantID == "" {
		return connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}
	annotators, err := s.loadQueueAnnotators(ctx, queueID, tenantID)
	if err != nil {
		return err
	}
	if !queueAnnotatorAuthorized(annotators, userID) {
		return connect.NewError(connect.CodePermissionDenied, errors.New("annotation queue access denied"))
	}
	return nil
}

func (s *Server) assertItemQueueAnnotator(ctx context.Context, itemID, tenantID, userID string) error {
	queueID, err := s.queueIDForItem(ctx, itemID, tenantID)
	if err != nil {
		return err
	}
	return s.assertQueueAnnotator(ctx, queueID, tenantID, userID)
}

func (s *Server) loadQueueAnnotators(ctx context.Context, queueID, tenantID string) ([]string, error) {
	if s.db != nil {
		var annotators pq.StringArray
		err := s.db.QueryRowxContext(ctx, `
			SELECT annotators
			FROM annotation_queues
			WHERE id = $1 AND tenant_id = $2
		`, queueID, tenantID).Scan(&annotators)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, connect.NewError(connect.CodeNotFound, errors.New("annotation queue not found"))
			}
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		return []string(annotators), nil
	}

	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}
	q := annotationsquery.NewGetQueueByIDQuery(queueID, tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if res == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("annotation queue not found"))
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}
	rm, ok := data.(*annotationsquery.QueueReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("unexpected data type"))
	}
	return []string(rm.Annotators), nil
}

func (s *Server) queueIDForItem(ctx context.Context, itemID, tenantID string) (string, error) {
	if tenantID == "" {
		return "", connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}
	if s.db == nil {
		return "", connect.NewError(connect.CodeInternal, errors.New("annotation authorization database not available"))
	}

	var queueID string
	err := s.db.GetContext(ctx, &queueID, `
		SELECT queue_id
		FROM annotation_queue_items
		WHERE id = $1 AND tenant_id = $2
	`, itemID, tenantID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", connect.NewError(connect.CodePermissionDenied, errors.New("annotation item access denied"))
		}
		return "", connect.NewError(connect.CodeInternal, err)
	}
	return queueID, nil
}
