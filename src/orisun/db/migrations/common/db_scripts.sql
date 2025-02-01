create table if not exists orisun_es_event (
    transaction_id xid8 not null,
    global_id bigint primary key,
    event_id uuid,
    event_type varchar(255) not null,
    data jsonb not null,
    metadata jsonb,
    date_created timestamp with time zone default now() not null,
    tags jsonb not null
);
create sequence if not exists orisun_es_event_global_id_seq;

create index if not exists idx_orisun_es_event_date_created on orisun_es_event (date_created);
create index if not exists idx_orisun_es_event_store_db_global_id on orisun_es_event (global_id);
create index if not exists idx_transaction_id_global_id on orisun_es_event (transaction_id, global_id);
create index if not exists idx_tags on orisun_es_event using gin (tags jsonb_path_ops);

-- Function to insert events with consistency checking and deadlock prevention.
-- Ensures that events are inserted only if they meet the specified consistency condition.
-- Uses advisory locks to prevent deadlocks and maintain consistency.
CREATE OR REPLACE FUNCTION insert_events_with_consistency(consistency_condition jsonb, events jsonb)
    RETURNS TABLE
            (
                latest_transaction_id xid8,
                latest_global_id      bigint,
                inserted              boolean
            )
    LANGUAGE plpgsql
AS
$$
DECLARE
    last_retrieved_transaction_id xid8;
    last_retrieved_global_id      bigint;
    lock_pairs                    JSONB   := '[]'::jsonb;
    criterion                     JSONB;
    lock_key                      integer;
    inserted_row                  boolean := FALSE;
    normalized_criterion          JSONB;
    normalized_criteria           JSONB   := '[]'::jsonb;
    original_criteria             JSONB   := consistency_condition -> 'criteria';
BEGIN
    -- Validate input JSON format
    IF consistency_condition ->> 'last_retrieved_position' IS NULL OR
       consistency_condition -> 'last_retrieved_position' ->> 'transaction_id' IS NULL OR
       consistency_condition -> 'last_retrieved_position' ->> 'global_id' IS NULL THEN
        RAISE EXCEPTION 'Invalid consistency_condition format';
    END IF;

    IF jsonb_array_length(events) = 0 THEN
        RAISE EXCEPTION 'Events array cannot be empty';
    END IF;

    -- Initialize last retrieved position
    last_retrieved_transaction_id :=
            ((consistency_condition ->> 'last_retrieved_position')::jsonb ->> 'transaction_id')::xid8;
    last_retrieved_global_id := ((consistency_condition ->> 'last_retrieved_position')::jsonb ->> 'global_id')::bigint;

    -- Normalize each criterion by sorting the keys and then sort the criteria array
    FOR criterion IN SELECT * FROM jsonb_array_elements(original_criteria)
    LOOP
            -- Sort keys within the criterion, with case normalization
        normalized_criterion := (SELECT jsonb_object_agg(lower(key), lower(value) ORDER BY lower(key), lower(value))
                                     FROM jsonb_each_text(criterion));

        -- Add normalized criterion to normalized_criteria array
        normalized_criteria := normalized_criteria || normalized_criterion;
    END LOOP;

    -- Sort the criteria array
    normalized_criteria := (SELECT jsonb_agg(value ORDER BY value)
                            FROM (SELECT jsonb_array_elements(normalized_criteria) AS value) AS sorted_criteria);

    -- Acquire locks for each criterion
    FOR criterion IN SELECT * FROM jsonb_array_elements(normalized_criteria)
        LOOP
            -- Create a consistent lock key by hashing the normalized criterion
            lock_key := hashtext(criterion::text);
            PERFORM pg_advisory_xact_lock(lock_key);

            -- Add the criterion to lock_pairs
--             lock_pairs := lock_pairs || jsonb_build_array(criterion);
        END LOOP;

    -- Retrieve the latest event that fulfills the consistency condition (using original case)
    SELECT transaction_id, global_id
    INTO latest_transaction_id, latest_global_id
    FROM orisun_es_event e
    WHERE e.tags @> ANY (ARRAY(SELECT jsonb_array_elements(original_criteria)))
    ORDER BY (transaction_id, global_id) DESC
    LIMIT 1;

    -- If no violating event is found, insert the new events
    IF (latest_global_id IS NULL OR
        (latest_transaction_id, latest_global_id) <= (last_retrieved_transaction_id, last_retrieved_global_id)) THEN
        -- Insert the events
        INSERT INTO orisun_es_event (transaction_id, event_type, "data", metadata, tags, event_id, date_created, global_id)
        SELECT pg_current_xact_id()::xid8,
               e.event ->> 'event_type',
               e.event -> 'data',
               e.event -> 'metadata',
               e.event -> 'tags',
               (e.event ->> 'event_id')::uuid,
               now(),
               nextval('orisun_es_event_global_id_seq')
        FROM jsonb_array_elements(events) AS e (event);

        -- Update the last retrieved position
        latest_transaction_id := pg_current_xact_id();
        latest_global_id := (SELECT max(global_id) FROM orisun_es_event WHERE transaction_id = latest_transaction_id);
        inserted_row := TRUE;
    ELSE
        -- If a conflicting event is found, raise an exception with details
        RAISE EXCEPTION 'OptimisticConcurrencyException: Inconsistent event stream. Latest conflicting event: transaction_id = %, global_id = %',
            latest_transaction_id, latest_global_id;
    END IF;

    RETURN QUERY SELECT latest_transaction_id, latest_global_id, inserted_row;
END;
$$;

CREATE OR REPLACE FUNCTION get_matching_events(
    criteria JSONB DEFAULT NULL,
    sort_order text DEFAULT 'ASC',
    i_limit integer DEFAULT NULL
)
    RETURNS TABLE
            (
                transaction_id xid8,
                global_id      bigint,
                event_type     text,
                data           JSONB,
                tags           JSONB,
                event_id       uuid,
                date_created   timestamptz,
                metadata       jsonb
            )
AS
$$
BEGIN
    -- Validate sort order
    IF sort_order NOT IN ('ASC', 'DESC') THEN
        RAISE LOG 'Invalid sort order. Must be either ''ASC'' or ''DESC''.';
    END IF;

    RETURN QUERY EXECUTE
        'SELECT transaction_id, global_id, event_type::text, data, tags, event_id, date_created, metadata
         FROM orisun_es_event e
         WHERE ($1->''last_retrieved_position'' IS NULL OR (
             CASE
                 WHEN $2 = ''ASC'' THEN (transaction_id, global_id) > (($1->''last_retrieved_position''->>''transaction_id'')::xid8, ($1->''last_retrieved_position''->>''global_id'')::bigint)
                 ELSE (transaction_id, global_id) < (($1->''last_retrieved_position''->>''transaction_id'')::xid8, ($1->''last_retrieved_position''->>''global_id'')::bigint)
             END
         ))
         AND ($1->''criteria'' IS NULL OR e.tags @> ANY (SELECT jsonb_path_query($1, ''$.criteria[*]'')))
         AND TRANSACTION_ID < pg_snapshot_xmin(pg_current_snapshot())
         ORDER BY transaction_id ' || sort_order || ', global_id ' || sort_order ||
        CASE
            WHEN $3 IS NOT NULL THEN
                ' LIMIT $3'
            ELSE
                ''
            END
        USING criteria, sort_order, i_limit;
END;
$$ LANGUAGE plpgsql;