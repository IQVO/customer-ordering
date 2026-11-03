CREATE TABLE compensation_cancels (
    id          bigserial   PRIMARY KEY,
    order_id    text        NOT NULL,
    group_no    integer     NOT NULL,
    promise_id  text        NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    released_at timestamptz,
    attempts    integer     NOT NULL DEFAULT 0,
    last_error  text,
    UNIQUE (order_id, group_no, promise_id)
);
CREATE INDEX compensation_cancels_pending_idx ON compensation_cancels (id) WHERE released_at IS NULL;
