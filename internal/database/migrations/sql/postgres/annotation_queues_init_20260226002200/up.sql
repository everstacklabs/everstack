-- Annotation Queues: Phase 2 - Annotation Queues & Feedback
-- Created: 2026-02-26

CREATE TABLE IF NOT EXISTS annotation_queues (
    id              VARCHAR(255) PRIMARY KEY,
    tenant_id       VARCHAR(255) NOT NULL,
    name            VARCHAR(512) NOT NULL,
    description     TEXT DEFAULT '',
    status          VARCHAR(50) DEFAULT 'active' CHECK (status IN ('active', 'paused', 'archived')),
    score_config_ids TEXT[] DEFAULT '{}',
    assignment_mode VARCHAR(50) DEFAULT 'manual' CHECK (assignment_mode IN ('manual', 'round_robin', 'random')),
    annotators      TEXT[] DEFAULT '{}',
    auto_populate_config JSONB DEFAULT '{}',
    items_pending   INT DEFAULT 0,
    items_completed INT DEFAULT 0,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(tenant_id, name)
);

CREATE TABLE IF NOT EXISTS annotation_queue_items (
    id              VARCHAR(255) PRIMARY KEY,
    queue_id        VARCHAR(255) NOT NULL REFERENCES annotation_queues(id) ON DELETE CASCADE,
    tenant_id       VARCHAR(255) NOT NULL,
    trace_id        VARCHAR(255) NOT NULL,
    observation_id  VARCHAR(255) DEFAULT '',
    assigned_to     VARCHAR(255) DEFAULT '',
    assigned_at     TIMESTAMPTZ,
    status          VARCHAR(50) DEFAULT 'pending' CHECK (status IN ('pending', 'in_progress', 'completed', 'skipped')),
    priority        INT DEFAULT 0,
    completed_by    VARCHAR(255) DEFAULT '',
    completed_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS annotation_item_scores (
    id              VARCHAR(255) PRIMARY KEY,
    queue_item_id   VARCHAR(255) NOT NULL REFERENCES annotation_queue_items(id) ON DELETE CASCADE,
    score_config_id VARCHAR(255) NOT NULL,
    score_id        VARCHAR(255) DEFAULT '',
    tenant_id       VARCHAR(255) NOT NULL,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

-- Indexes for annotation_queues
CREATE INDEX IF NOT EXISTS idx_annotation_queues_tenant_id ON annotation_queues(tenant_id);
CREATE INDEX IF NOT EXISTS idx_annotation_queues_status ON annotation_queues(status);

-- Indexes for annotation_queue_items
CREATE INDEX IF NOT EXISTS idx_annotation_queue_items_tenant_id ON annotation_queue_items(tenant_id);
CREATE INDEX IF NOT EXISTS idx_annotation_queue_items_queue_id ON annotation_queue_items(queue_id);
CREATE INDEX IF NOT EXISTS idx_annotation_queue_items_status ON annotation_queue_items(status);
CREATE INDEX IF NOT EXISTS idx_annotation_queue_items_assigned_to ON annotation_queue_items(assigned_to);
CREATE INDEX IF NOT EXISTS idx_annotation_queue_items_queue_status ON annotation_queue_items(queue_id, status);

-- Indexes for annotation_item_scores
CREATE INDEX IF NOT EXISTS idx_annotation_item_scores_tenant_id ON annotation_item_scores(tenant_id);
CREATE INDEX IF NOT EXISTS idx_annotation_item_scores_queue_item_id ON annotation_item_scores(queue_item_id);
