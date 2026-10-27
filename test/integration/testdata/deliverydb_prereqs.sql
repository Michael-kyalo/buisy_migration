-- Test-only stand-in for the trigger function schema/deliverydb.sql references
-- but doesn't define.
CREATE FUNCTION log_status_to_intransit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RETURN NEW;
END
$$;
