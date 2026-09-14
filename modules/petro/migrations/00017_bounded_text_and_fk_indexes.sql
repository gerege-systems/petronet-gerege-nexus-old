-- Хязгааргүй TEXT багана ба индексгүй гадаад түлхүүр (аудит 2026-09-14).
--
-- # Текстийн урт
--
-- Модулийн бүх мөр багана TEXT байсан. Handler-ууд body-г 2–4 КБ-аар хаадаг ч
-- тэр нь нэг хүсэлтийн хязгаар, баганын биш: хөдөлгөөний тэмдэглэл `||`-ээр
-- нэмэгддэг тул дуудлага бүр түүнийг өсгөнө, импорт ба шууд SQL body-ийн
-- хязгаарыг огт мэдэхгүй. Төрлийг солихын оронд CHECK: `varchar(n)` руу ALTER
-- нь хүснэгтийг дахин бичиж ACCESS EXCLUSIVE түгжээ барина, CHECK-ийг NOT VALID
-- -ээр нэмээд VALIDATE хийх нь бичилтийг хаахгүй.
--
-- Хэмжээ нь утгын төрлөөр, багана бүрээр биш:
--   64   — код, төлөв, төрөл (fuel_type, status, scope, severity …)
--   128  — дугаар, лавлагаа, токен (trip_code, national_code, qr_token …)
--   256  — нэр, шошго, газар (name, fuel_label, aimag, driver_name …)
--   512  — хаяг
--   4000 — чөлөөт тэмдэглэл (note, review_note, message)
--
-- # Гадаад түлхүүрийн индекс
--
-- Postgres FK-ийн хүүхэд талд индекс үүсгэдэггүй. Эцэг мөрийг устгах, cascade,
-- RLS-ийн tenant_id шүүлт бүр тэр хүснэгтийг бүтнээр нь уншина. Одоо жижиг
-- ч `petro_report_lines`, `petro_vouchers` хамгийн хурдан өсдөг.

-- +goose Up

-- +goose StatementBegin
DO $$
DECLARE
    spec RECORD;
BEGIN
    FOR spec IN
        SELECT * FROM (VALUES
            ('petro_batches', 'batch_code', 128), ('petro_batches', 'fuel_type', 64),
            ('petro_batches', 'fuel_label', 256), ('petro_batches', 'origin_country', 256),
            ('petro_batches', 'refinery', 256), ('petro_batches', 'customs_decl_no', 128),
            ('petro_batches', 'quality_cert_no', 128), ('petro_batches', 'lab_status', 64),

            ('petro_customs_shipments', 'declaration_no', 128), ('petro_customs_shipments', 'border_port', 256),
            ('petro_customs_shipments', 'origin_country', 256), ('petro_customs_shipments', 'exporter', 256),
            ('petro_customs_shipments', 'fuel_type', 64), ('petro_customs_shipments', 'fuel_label', 256),
            ('petro_customs_shipments', 'convoy_code', 128), ('petro_customs_shipments', 'status', 64),
            ('petro_customs_shipments', 'lab_status', 64), ('petro_customs_shipments', 'quality_cert_no', 128),
            ('petro_customs_shipments', 'note', 4000),

            ('petro_daily_national', 'product_code', 64), ('petro_daily_national', 'aimag', 256),

            ('petro_depot_receipts', 'note', 4000),

            ('petro_depot_tanks', 'tank_no', 128), ('petro_depot_tanks', 'tank_type', 64),
            ('petro_depot_tanks', 'fuel_type', 64), ('petro_depot_tanks', 'fuel_label', 256),
            ('petro_depot_tanks', 'safety_status', 64),

            ('petro_depots', 'name', 256), ('petro_depots', 'brand', 256), ('petro_depots', 'aimag', 256),
            ('petro_depots', 'district', 256), ('petro_depots', 'address', 512),
            ('petro_depots', 'rail_station_code', 128), ('petro_depots', 'status', 64),
            ('petro_depots', 'source', 64), ('petro_depots', 'source_ref', 128),
            ('petro_depots', 'national_code', 128), ('petro_depots', 'registry_status', 64),

            ('petro_devices', 'site_kind', 64), ('petro_devices', 'serial', 128),
            ('petro_devices', 'vendor', 256), ('petro_devices', 'protocol', 64), ('petro_devices', 'status', 64),

            ('petro_dispatch_trips', 'trip_code', 128), ('petro_dispatch_trips', 'tanker_plate', 128),
            ('petro_dispatch_trips', 'driver_name', 256), ('petro_dispatch_trips', 'driver_phone', 128),
            ('petro_dispatch_trips', 'from_depot', 256), ('petro_dispatch_trips', 'fuel_type', 64),
            ('petro_dispatch_trips', 'fuel_label', 256), ('petro_dispatch_trips', 'seal_no', 128),
            ('petro_dispatch_trips', 'seal_status', 64), ('petro_dispatch_trips', 'status', 64),
            ('petro_dispatch_trips', 'source', 64), ('petro_dispatch_trips', 'source_ref', 128),

            ('petro_movements', 'national_ref', 128), ('petro_movements', 'from_kind', 64),
            ('petro_movements', 'to_kind', 64), ('petro_movements', 'product_code', 64),
            ('petro_movements', 'status', 64), ('petro_movements', 'note', 4000),

            ('petro_oversight_bodies', 'name', 256), ('petro_oversight_bodies', 'scope', 64),
            ('petro_oversight_bodies', 'aimag', 256),

            ('petro_policy', 'note', 4000),

            ('petro_products', 'code', 64), ('petro_products', 'label_mn', 256),
            ('petro_products', 'label_en', 256), ('petro_products', 'jodi_category', 64),

            ('petro_report_lines', 'site_kind', 64), ('petro_report_lines', 'product_code', 64),
            ('petro_report_lines', 'note', 4000),

            ('petro_report_periods', 'kind', 64), ('petro_report_periods', 'status', 64),

            ('petro_report_submissions', 'status', 64), ('petro_report_submissions', 'source', 64),
            ('petro_report_submissions', 'file_name', 256), ('petro_report_submissions', 'review_note', 4000),
            ('petro_report_submissions', 'idempotency_key', 128),

            ('petro_station_inventory', 'fuel_type', 64), ('petro_station_inventory', 'fuel_label', 256),
            ('petro_station_inventory', 'status', 64),

            ('petro_station_receipts', 'fuel_type', 64), ('petro_station_receipts', 'seal_status', 64),
            ('petro_station_receipts', 'note', 4000),

            ('petro_stations', 'name', 256), ('petro_stations', 'brand', 256),
            ('petro_stations', 'brand_label', 256), ('petro_stations', 'aimag', 256),
            ('petro_stations', 'district', 256), ('petro_stations', 'address', 512),
            ('petro_stations', 'phone', 128), ('petro_stations', 'opening_hours', 256),
            ('petro_stations', 'status', 64), ('petro_stations', 'source', 64),
            ('petro_stations', 'source_ref', 128), ('petro_stations', 'national_code', 128),
            ('petro_stations', 'registry_status', 64), ('petro_stations', 'pump_brand', 256),
            ('petro_stations', 'pump_protocol', 64),

            ('petro_validation_findings', 'rule', 64), ('petro_validation_findings', 'severity', 64),
            ('petro_validation_findings', 'message', 4000),

            ('petro_vouchers', 'fuel_type', 64), ('petro_vouchers', 'fuel_label', 256),
            ('petro_vouchers', 'qr_token', 128), ('petro_vouchers', 'status', 64)
        ) AS v(tbl, col, max_len)
    LOOP
        -- Багана өмнөх миграцаар устсан бол алгасна: энэ жагсаалт 00015 хүртэлх
        -- схемээс гарсан.
        CONTINUE WHEN NOT EXISTS (
            SELECT 1 FROM information_schema.columns
             WHERE table_schema = 'workspace' AND table_name = spec.tbl AND column_name = spec.col);
        EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I CHECK (char_length(%I) <= %s) NOT VALID',
                       spec.tbl, spec.tbl || '_' || spec.col || '_len', spec.col, spec.max_len);
        EXECUTE format('ALTER TABLE %I VALIDATE CONSTRAINT %I',
                       spec.tbl, spec.tbl || '_' || spec.col || '_len');
    END LOOP;
END;
$$;
-- +goose StatementEnd

CREATE INDEX IF NOT EXISTS idx_petro_daily_national_product ON petro_daily_national (product_code);
CREATE INDEX IF NOT EXISTS idx_petro_depot_receipts_tank ON petro_depot_receipts (tank_id);
CREATE INDEX IF NOT EXISTS idx_petro_depot_receipts_tenant ON petro_depot_receipts (tenant_id);
CREATE INDEX IF NOT EXISTS idx_petro_depot_tanks_tenant ON petro_depot_tanks (tenant_id);
CREATE INDEX IF NOT EXISTS idx_petro_devices_tank ON petro_devices (tank_id);
CREATE INDEX IF NOT EXISTS idx_petro_dispatch_trips_from_tank ON petro_dispatch_trips (from_tank_id);
CREATE INDEX IF NOT EXISTS idx_petro_entitlements_tenant ON petro_entitlements (tenant_id);
CREATE INDEX IF NOT EXISTS idx_petro_movements_product ON petro_movements (product_code);
CREATE INDEX IF NOT EXISTS idx_petro_movements_trip ON petro_movements (trip_id);
CREATE INDEX IF NOT EXISTS idx_petro_report_lines_product ON petro_report_lines (product_code);
CREATE INDEX IF NOT EXISTS idx_petro_report_lines_tenant ON petro_report_lines (tenant_id);
CREATE INDEX IF NOT EXISTS idx_petro_report_submissions_period ON petro_report_submissions (period_id);
CREATE INDEX IF NOT EXISTS idx_petro_station_receipts_tenant ON petro_station_receipts (tenant_id);
CREATE INDEX IF NOT EXISTS idx_petro_validation_findings_line ON petro_validation_findings (line_id);
CREATE INDEX IF NOT EXISTS idx_petro_validation_findings_tenant ON petro_validation_findings (tenant_id);
CREATE INDEX IF NOT EXISTS idx_petro_vouchers_tenant ON petro_vouchers (tenant_id);

-- Маргааны тэмдэглэл хуучин тэмдэглэл дээр залгагддаг: тус бүр нь 4000-д
-- багтсан ч нийлбэр нь хэтэрч, дээрх CHECK-ээр 23514 болж хөдөлгөөнийг цаашид
-- маргах боломжгүй болгоно. Сүүлийн 4000 тэмдэгтийг үлдээнэ — шинэ шалтгаан
-- нь хамгийн сүүлд, түүнийг хаяхгүй.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION petro_dispute_movement(movement UUID, dispute_note TEXT)
RETURNS TEXT
LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path = workspace, registry, pg_catalog, pg_temp
AS $$
DECLARE
    ref TEXT;
BEGIN
    IF petro_oversight_scope() IS DISTINCT FROM 'national' THEN
        RAISE EXCEPTION 'not a national oversight body' USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF COALESCE(dispute_note, '') = '' THEN
        RAISE EXCEPTION 'a dispute needs a reason' USING ERRCODE = 'check_violation';
    END IF;

    UPDATE petro_movements m
       SET status = 'disputed',
           note = right(m.note || CASE WHEN m.note = '' THEN '' ELSE ' · ' END || dispute_note, 4000)
     WHERE m.id = movement AND m.status IN ('open', 'closed')
    RETURNING m.national_ref INTO ref;

    RETURN ref;
END;
$$;
-- +goose StatementEnd

-- Өдрийн нэгтгэлийг зэрэг хоёр удаа тооцоолох (replica бүрийн цагийн ажил ба
-- «дахин тооцоол» товч) нь DELETE + INSERT хоёрын хооронд PK зөрчил (23505)
-- өгч нэгийг нь унагадаг байв. Өдөр тутмын advisory lock нь тэднийг дараалуулна.
-- Биеийг 00016-аас хуулав; нэмэгдсэн нь зөвхөн PERFORM мөр.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION petro_refresh_daily(for_day DATE)
RETURNS INT
LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path = workspace, registry, pg_catalog, pg_temp
AS $$
DECLARE
    written INT;
BEGIN
    -- Тенантгүй дуудлага нь хуваарьт ажил; тенанттай дуудлага нь зөвхөн
    -- үндэсний хяналтын байгууллагынх.
    IF COALESCE(NULLIF(current_setting('app.current_tenant', true), ''), '') <> ''
       AND petro_oversight_scope() IS DISTINCT FROM 'national' THEN
        RAISE EXCEPTION 'not a national oversight body' USING ERRCODE = 'insufficient_privilege';
    END IF;

    PERFORM pg_advisory_xact_lock(hashtext('petro_refresh_daily'), for_day - DATE '2000-01-01');

    DELETE FROM petro_daily_national WHERE day = for_day;

    WITH day_lines AS (
        SELECT DISTINCT ON (l.site_kind, l.site_id, l.product_code)
               l.site_kind, l.site_id, l.product_code,
               l.closing_liters, l.closing_liters_15c, l.receipts_liters, l.sales_liters
          FROM petro_report_lines l
          JOIN petro_report_submissions s ON s.id = l.submission_id
          JOIN petro_report_periods p ON p.id = s.period_id
         WHERE p.period_start = for_day
           AND s.status IN ('submitted', 'approved')
         ORDER BY l.site_kind, l.site_id, l.product_code, s.version DESC
    ),
    week_sales AS (
        SELECT l.site_kind, l.site_id, l.product_code, AVG(l.sales_liters) AS avg_sales
          FROM petro_report_lines l
          JOIN petro_report_submissions s ON s.id = l.submission_id
          JOIN petro_report_periods p ON p.id = s.period_id
         WHERE p.period_start BETWEEN (for_day - 6) AND for_day
           AND s.status IN ('submitted', 'approved')
         GROUP BY l.site_kind, l.site_id, l.product_code
    ),
    site_aimag AS (
        SELECT 'station'::text AS site_kind, id AS site_id,
               COALESCE(NULLIF(aimag, ''), '—') AS aimag
          FROM petro_stations WHERE registry_status <> 'closed'
        UNION ALL
        SELECT 'depot', id, COALESCE(NULLIF(aimag, ''), '—')
          FROM petro_depots WHERE registry_status <> 'closed'
    ),
    site_products AS (
        SELECT 'station'::text AS site_kind, station_id AS site_id, fuel_type AS product_code,
               tank_capacity_liters AS capacity_liters
          FROM petro_station_inventory
        UNION ALL
        SELECT 'depot', depot_id, fuel_type, SUM(capacity_liters)
          FROM petro_depot_tanks GROUP BY 1, 2, 3
    ),
    expected AS (
        SELECT sp.site_kind, sp.site_id, sp.product_code, sa.aimag, sp.capacity_liters
          FROM site_products sp
          JOIN site_aimag sa ON sa.site_kind = sp.site_kind AND sa.site_id = sp.site_id
          JOIN petro_products pr ON pr.code = sp.product_code
    )
    INSERT INTO petro_daily_national
           (day, product_code, aimag, stock_liters, capacity_liters, receipts_liters,
            sales_liters, sites_total, sites_reported, days_of_supply, refreshed_at)
    SELECT for_day, e.product_code, e.aimag,
           COALESCE(SUM(COALESCE(d.closing_liters_15c, d.closing_liters)), 0),
           COALESCE(SUM(e.capacity_liters), 0),
           COALESCE(SUM(d.receipts_liters), 0),
           COALESCE(SUM(d.sales_liters), 0),
           COUNT(*), COUNT(d.site_id),
           CASE WHEN COALESCE(SUM(w.avg_sales), 0) > 0
                THEN COALESCE(SUM(COALESCE(d.closing_liters_15c, d.closing_liters)), 0)
                     / SUM(w.avg_sales) END,
           NOW()
      FROM expected e
      LEFT JOIN day_lines d
             ON d.site_kind = e.site_kind AND d.site_id = e.site_id
            AND d.product_code = e.product_code
      LEFT JOIN week_sales w
             ON w.site_kind = e.site_kind AND w.site_id = e.site_id
            AND w.product_code = e.product_code
     GROUP BY e.product_code, e.aimag;

    GET DIAGNOSTICS written = ROW_COUNT;
    RETURN written;
END;
$$;
-- +goose StatementEnd

-- +goose Down

-- Хоёр функцийг 00016-ийн хэлбэрт нь буцаана (CREATE OR REPLACE эрхийг хадгална).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION petro_dispute_movement(movement UUID, dispute_note TEXT)
RETURNS TEXT
LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path = workspace, registry, pg_catalog, pg_temp
AS $$
DECLARE
    ref TEXT;
BEGIN
    IF petro_oversight_scope() IS DISTINCT FROM 'national' THEN
        RAISE EXCEPTION 'not a national oversight body' USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF COALESCE(dispute_note, '') = '' THEN
        RAISE EXCEPTION 'a dispute needs a reason' USING ERRCODE = 'check_violation';
    END IF;

    UPDATE petro_movements m
       SET status = 'disputed',
           note = m.note || CASE WHEN m.note = '' THEN '' ELSE ' · ' END || dispute_note
     WHERE m.id = movement AND m.status IN ('open', 'closed')
    RETURNING m.national_ref INTO ref;

    RETURN ref;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION petro_refresh_daily(for_day DATE)
RETURNS INT
LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path = workspace, registry, pg_catalog, pg_temp
AS $$
DECLARE
    written INT;
BEGIN
    IF COALESCE(NULLIF(current_setting('app.current_tenant', true), ''), '') <> ''
       AND petro_oversight_scope() IS DISTINCT FROM 'national' THEN
        RAISE EXCEPTION 'not a national oversight body' USING ERRCODE = 'insufficient_privilege';
    END IF;

    DELETE FROM petro_daily_national WHERE day = for_day;

    WITH day_lines AS (
        SELECT DISTINCT ON (l.site_kind, l.site_id, l.product_code)
               l.site_kind, l.site_id, l.product_code,
               l.closing_liters, l.closing_liters_15c, l.receipts_liters, l.sales_liters
          FROM petro_report_lines l
          JOIN petro_report_submissions s ON s.id = l.submission_id
          JOIN petro_report_periods p ON p.id = s.period_id
         WHERE p.period_start = for_day
           AND s.status IN ('submitted', 'approved')
         ORDER BY l.site_kind, l.site_id, l.product_code, s.version DESC
    ),
    week_sales AS (
        SELECT l.site_kind, l.site_id, l.product_code, AVG(l.sales_liters) AS avg_sales
          FROM petro_report_lines l
          JOIN petro_report_submissions s ON s.id = l.submission_id
          JOIN petro_report_periods p ON p.id = s.period_id
         WHERE p.period_start BETWEEN (for_day - 6) AND for_day
           AND s.status IN ('submitted', 'approved')
         GROUP BY l.site_kind, l.site_id, l.product_code
    ),
    site_aimag AS (
        SELECT 'station'::text AS site_kind, id AS site_id,
               COALESCE(NULLIF(aimag, ''), '—') AS aimag
          FROM petro_stations WHERE registry_status <> 'closed'
        UNION ALL
        SELECT 'depot', id, COALESCE(NULLIF(aimag, ''), '—')
          FROM petro_depots WHERE registry_status <> 'closed'
    ),
    site_products AS (
        SELECT 'station'::text AS site_kind, station_id AS site_id, fuel_type AS product_code,
               tank_capacity_liters AS capacity_liters
          FROM petro_station_inventory
        UNION ALL
        SELECT 'depot', depot_id, fuel_type, SUM(capacity_liters)
          FROM petro_depot_tanks GROUP BY 1, 2, 3
    ),
    expected AS (
        SELECT sp.site_kind, sp.site_id, sp.product_code, sa.aimag, sp.capacity_liters
          FROM site_products sp
          JOIN site_aimag sa ON sa.site_kind = sp.site_kind AND sa.site_id = sp.site_id
          JOIN petro_products pr ON pr.code = sp.product_code
    )
    INSERT INTO petro_daily_national
           (day, product_code, aimag, stock_liters, capacity_liters, receipts_liters,
            sales_liters, sites_total, sites_reported, days_of_supply, refreshed_at)
    SELECT for_day, e.product_code, e.aimag,
           COALESCE(SUM(COALESCE(d.closing_liters_15c, d.closing_liters)), 0),
           COALESCE(SUM(e.capacity_liters), 0),
           COALESCE(SUM(d.receipts_liters), 0),
           COALESCE(SUM(d.sales_liters), 0),
           COUNT(*), COUNT(d.site_id),
           CASE WHEN COALESCE(SUM(w.avg_sales), 0) > 0
                THEN COALESCE(SUM(COALESCE(d.closing_liters_15c, d.closing_liters)), 0)
                     / SUM(w.avg_sales) END,
           NOW()
      FROM expected e
      LEFT JOIN day_lines d
             ON d.site_kind = e.site_kind AND d.site_id = e.site_id
            AND d.product_code = e.product_code
      LEFT JOIN week_sales w
             ON w.site_kind = e.site_kind AND w.site_id = e.site_id
            AND w.product_code = e.product_code
     GROUP BY e.product_code, e.aimag;

    GET DIAGNOSTICS written = ROW_COUNT;
    RETURN written;
END;
$$;
-- +goose StatementEnd

DROP INDEX IF EXISTS idx_petro_vouchers_tenant;
DROP INDEX IF EXISTS idx_petro_validation_findings_tenant;
DROP INDEX IF EXISTS idx_petro_validation_findings_line;
DROP INDEX IF EXISTS idx_petro_station_receipts_tenant;
DROP INDEX IF EXISTS idx_petro_report_submissions_period;
DROP INDEX IF EXISTS idx_petro_report_lines_tenant;
DROP INDEX IF EXISTS idx_petro_report_lines_product;
DROP INDEX IF EXISTS idx_petro_movements_trip;
DROP INDEX IF EXISTS idx_petro_movements_product;
DROP INDEX IF EXISTS idx_petro_entitlements_tenant;
DROP INDEX IF EXISTS idx_petro_dispatch_trips_from_tank;
DROP INDEX IF EXISTS idx_petro_devices_tank;
DROP INDEX IF EXISTS idx_petro_depot_tanks_tenant;
DROP INDEX IF EXISTS idx_petro_depot_receipts_tenant;
DROP INDEX IF EXISTS idx_petro_depot_receipts_tank;
DROP INDEX IF EXISTS idx_petro_daily_national_product;

-- +goose StatementBegin
DO $$
DECLARE
    con RECORD;
BEGIN
    FOR con IN
        SELECT t.relname AS tbl, k.conname
          FROM pg_constraint k
          JOIN pg_class t ON t.oid = k.conrelid
          JOIN pg_namespace n ON n.oid = t.relnamespace
         WHERE n.nspname = 'workspace' AND t.relname LIKE 'petro\_%'
           AND k.contype = 'c' AND k.conname LIKE '%\_len'
    LOOP
        EXECUTE format('ALTER TABLE %I DROP CONSTRAINT %I', con.tbl, con.conname);
    END LOOP;
END;
$$;
-- +goose StatementEnd
