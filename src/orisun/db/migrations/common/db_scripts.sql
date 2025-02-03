CREATE EXTENSION IF NOT EXISTS btree_gin;

-- Table & Sequence
CREATE TABLE IF NOT EXISTS orisun_es_event (
    transaction_id xid8 NOT NULL,
    global_id BIGINT PRIMARY KEY,
    stream_name TEXT NOT NULL,
    stream_version BIGINT NOT NULL,
    event_id UUID NOT NULL,
    event_type TEXT NOT NULL CHECK (event_type <> ''),
    data JSONB NOT NULL,
    metadata JSONB,
    date_created TIMESTAMPTZ DEFAULT NOW() NOT NULL,
    tags JSONB NOT NULL
);

CREATE SEQUENCE IF NOT EXISTS orisun_es_event_global_id_seq 
OWNED BY orisun_es_event.global_id;

-- Indexes
CREATE INDEX IF NOT EXISTS idx_stream ON orisun_es_event (stream_name);
CREATE INDEX IF NOT EXISTS idx_stream_version ON orisun_es_event (stream_name, stream_version);
CREATE INDEX IF NOT EXISTS idx_es_event_tags ON orisun_es_event USING GIN (tags jsonb_path_ops);
CREATE INDEX IF NOT EXISTS idx_global_order ON orisun_es_event (transaction_id, global_id);
CREATE INDEX IF NOT EXISTS idx_stream_tags ON orisun_es_event 
  USING GIN (stream_name, tags jsonb_path_ops);

-- Insert Function (With Improved Locking)
CREATE OR REPLACE FUNCTION insert_events_with_consistency(
    stream_info JSONB,
    global_condition JSONB,
    events JSONB
) RETURNS TABLE (
    new_stream_version BIGINT,
    latest_transaction_id xid8,
    latest_global_id BIGINT
) LANGUAGE plpgsql AS $$
DECLARE
    stream TEXT := stream_info ->> 'stream_name';
    expected_stream_version BIGINT := (stream_info ->> 'expected_version')::BIGINT;
    stream_criteria JSONB := stream_info -> 'criteria';
    
    last_position JSONB := global_condition -> 'last_retrieved_position';
    global_criteria JSONB := global_condition -> 'criteria';
    
    current_tx_id xid8 := pg_current_xact_id();
    current_stream_version BIGINT := 0;
    conflict_transaction xid8;
    conflict_global_id BIGINT;
    global_keys TEXT[];
    key_record TEXT;
BEGIN
    IF jsonb_array_length(events) = 0 THEN
        RAISE EXCEPTION 'Events array cannot be empty';
    END IF;

    PERFORM pg_advisory_xact_lock(hashtext(stream));

    -- Stream version check
    SELECT (oe.stream_version)
    INTO current_stream_version
    FROM orisun_es_event oe
    WHERE oe.stream_name = stream
      AND (stream_criteria IS NULL OR tags @> ANY (
          SELECT jsonb_array_elements(stream_criteria)
      ))
    ORDER BY (transaction_id, global_id) DESC
    LIMIT 1;

    IF current_stream_version IS NULL THEN
        current_stream_version := 0;
    END IF;

    IF current_stream_version <> expected_stream_version THEN
        RAISE EXCEPTION 'OptimisticConcurrencyException:StreamVersionConflict: Expected %, Actual %',
            expected_stream_version, current_stream_version;
    END IF;

    IF global_criteria IS NOT NULL THEN
        -- Extract all unique criteria key-value pairs
        SELECT ARRAY_AGG(DISTINCT format('%s:%s', key, value))
        INTO global_keys
        FROM jsonb_array_elements(global_criteria) AS criterion,
            jsonb_each_text(criterion) AS key_value;

        -- Lock key-value pairs in alphabetical order (deadlock prevention)
        IF global_keys IS NOT NULL THEN
            global_keys := ARRAY(
                SELECT DISTINCT unnest(global_keys)
                ORDER BY 1  -- Alphabetical sort
            );
            
            FOREACH key_record IN ARRAY global_keys
            LOOP
                PERFORM pg_advisory_xact_lock(hashtext(key_record));
            END LOOP;
        END IF;

        -- Global position check
        IF last_position IS NOT NULL THEN
            SELECT e.transaction_id, e.global_id 
            INTO conflict_transaction, conflict_global_id
            FROM orisun_es_event e
            WHERE (e.transaction_id, e.global_id) > (
                (last_position->>'transaction_id')::xid8,
                (last_position->>'global_id')::bigint
            )
            AND e.tags @> ANY (SELECT jsonb_array_elements(global_criteria))
            ORDER BY e.transaction_id DESC, e.global_id DESC
            LIMIT 1;

            IF FOUND THEN
                RAISE EXCEPTION 'OptimisticConcurrencyException: Global Conflict: Position %/%',
                    conflict_transaction, conflict_global_id;
            END IF;
        END IF;
    END IF;

    -- select the frontier of the stream if a subset criteria was specified to ensure the next set of events are properly versioned
    IF stream_criteria IS NOT NULL THEN
        SELECT (oe.stream_version)
            INTO current_stream_version
        FROM orisun_es_event oe
        WHERE oe.stream_name = stream
        ORDER BY oe.version DESC
        LIMIT 1;
    END IF;

    WITH inserted_events AS (
        INSERT INTO orisun_es_event (
            stream_name,
            stream_version,
            transaction_id,
            event_id,
            global_id,
            event_type,
            data,
            metadata,
            tags
        )
        SELECT
            stream,
            current_stream_version + ROW_NUMBER() OVER (),
            current_tx_id,
            (e->>'event_id')::UUID,
            nextval('orisun_es_event_global_id_seq'),
            e->>'event_type',
            COALESCE(e->'data', '{}'),
            COALESCE(e->'metadata', '{}'),
            COALESCE(e->'tags', '{}')
        FROM jsonb_array_elements(events) AS e
        RETURNING jsonb_array_length(events), global_id
    )
    SELECT current_stream_version + jsonb_array_length(events), current_tx_id, MAX(global_id)
    INTO new_stream_version, latest_transaction_id, latest_global_id
    FROM inserted_events;

    RETURN QUERY SELECT new_stream_version, latest_transaction_id, latest_global_id;
END;
$$;

-- Query Function
CREATE OR REPLACE FUNCTION get_matching_events(
    stream_name TEXT DEFAULT NULL,
    criteria JSONB DEFAULT NULL,
    after_position JSONB DEFAULT NULL,
    sort_dir TEXT DEFAULT 'ASC',
    max_count INT DEFAULT 1000
) RETURNS SETOF orisun_es_event 
LANGUAGE plpgsql STABLE AS $$
DECLARE
    op TEXT := CASE WHEN sort_dir = 'ASC' THEN '>' ELSE '<' END;
BEGIN
    IF sort_dir NOT IN ('ASC','DESC') THEN
        RAISE EXCEPTION 'Invalid sort direction: "%"', sort_dir;
    END IF;

    RETURN QUERY EXECUTE format(
        $q$
        SELECT * FROM orisun_es_event
        WHERE 
            (%1$L IS NULL OR stream_name = %2$L) AND
            (%3$L IS NULL OR 
             (transaction_id, global_id) %4$s (
                %5$L::xid8, 
                %6$L::BIGINT
             )) AND
            (%7$L::JSONB IS NULL OR tags @> ANY (
                SELECT jsonb_array_elements(%7$L)
            ))
        ORDER BY transaction_id %8$s, global_id %8$s
        LIMIT %9$L
        $q$,
        stream_name IS NOT NULL,
        stream_name,
        after_position IS NOT NULL,
        op,
        (after_position->>'transaction_id')::text,
        (after_position->>'global_id')::text,
        criteria,
        sort_dir,
        LEAST(GREATEST(max_count, 1), 10000)
    );
END;
$$;