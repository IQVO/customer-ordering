DROP INDEX IF EXISTS idx_analytics_processed_events_occurred_at;
DROP INDEX IF EXISTS idx_shopper_notices_decided_at;
DROP INDEX IF EXISTS idx_order_cancellations_cancelled_at;
DROP INDEX IF EXISTS idx_placed_orders_placed_at;
DROP TABLE IF EXISTS shopper_notices;
DROP TABLE IF EXISTS order_cancellations;
DROP TABLE IF EXISTS placed_orders;
DROP TABLE IF EXISTS analytics_processed_events;
