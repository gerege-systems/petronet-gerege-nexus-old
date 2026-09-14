-- Хоёр компанийн хооронд болдог үйлдлүүд — аудитын 2-р багц (2026-09-14).
--
-- # Юу буруу байсан бэ
--
-- 1. Хөдөлгөөн. movement.go «илгээгч нээж, хүлээн авагч хаана» гэдэг ч мөр нь
--    илгээгчийн тенантынх. Хүлээн авагч компани `tenant_isolation`-д тэр мөрийг
--    хардаггүй тул 404 авна, харин ИЛГЭЭГЧ өөрийн хөдөлгөөнийг дурын
--    `received_liters`-ээр хааж чадна — зөрүүгээ өөрөө бичдэг тал. Дээр нь
--    `oversight_close` нь хяналтын байгууллагад мөрийн БҮХ баганыг засах эрх
--    өгдөг байв (00011-ийн №4-тэй ижил алдаа).
-- 2. Өөр компанийн ШТС рүү ачилт. `to_station_id`-ийн FK нь RLS-ийг тойрдог тул
--    рейс өөр компанийн ШТС рүү чиглэж болно. Дараа нь хүлээн авагч рейсийг
--    харахгүй (404), илгээгч хүлээн авбал `petro_station_inventory`-ийн RLS
--    унагаана (500) — агуулахын сав аль хэдийн хасагдсан тул литр мөнхөд
--    «замд» үлдэнэ.
-- 3. `oversight_review` нь ирүүлэлтийн бүх баганыг (hash, prev_hash, seq,
--    tenant_id, мэдүүлсэн тоо) засах эрх байв.
-- 4. `petro_oversight_bodies.scope` хэзээ ч шалгагдаагүй: аудитын (зөвхөн
--    унших) байгууллага ШТС түдгэлзүүлж, тайлан баталж чаддаг байв.
-- 5. Ваучерыг UUID-гаар нь дангаар хааж болдог байв — UUID аудитын логт
--    бичигддэг. QR код заавал.
--
-- # Загвар нь 00011, 00014-тэй ижил
--
-- Нэг зүйл хийдэг, дуудагчаа өөрөө шалгадаг SECURITY DEFINER функц; мөрийн
-- бодлого өргөсгөхгүй. Хүлээн авагч үйлдэл хийнэ — хүлээн авагч гэдэг нь
-- очих объект (ШТС/бааз) нь дуудагчийн тенантынх.

-- +goose Up

-- ─────────────────────────────────────────────────────────────────────────
-- №4 — хяналтын байгууллагын хүрээ
-- ─────────────────────────────────────────────────────────────────────────
--
-- Зөвхөн 'national' бичнэ. 'audit', 'tax', 'customs', 'aimag' нь харах эрхтэй
-- хэвээр — унших бодлогууд `petro_is_oversight()`-ийг ашигласаар.

-- +goose StatementBegin
CREATE FUNCTION petro_oversight_scope() RETURNS TEXT
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = workspace, registry, pg_catalog, pg_temp
AS $$
    SELECT scope FROM petro_oversight_bodies
     WHERE tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION petro_oversight_scope() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION petro_oversight_scope() TO gerege_nexus_tenant;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION petro_set_site_status(site_kind TEXT, site UUID, new_status TEXT)
RETURNS TEXT
LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path = workspace, registry, pg_catalog, pg_temp
AS $$
DECLARE
    site_name TEXT;
BEGIN
    IF petro_oversight_scope() IS DISTINCT FROM 'national' THEN
        RAISE EXCEPTION 'not a national oversight body' USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF new_status NOT IN ('active', 'suspended', 'closed') THEN
        RAISE EXCEPTION 'unknown status %', new_status USING ERRCODE = 'check_violation';
    END IF;

    IF site_kind = 'station' THEN
        UPDATE petro_stations SET registry_status = new_status, updated_at = NOW()
         WHERE id = site RETURNING name INTO site_name;
    ELSIF site_kind = 'depot' THEN
        UPDATE petro_depots SET registry_status = new_status, updated_at = NOW()
         WHERE id = site RETURNING name INTO site_name;
    ELSE
        RAISE EXCEPTION 'unknown site kind %', site_kind USING ERRCODE = 'check_violation';
    END IF;

    RETURN site_name;
END;
$$;
-- +goose StatementEnd

-- Биеийг 00011-ээс хуулав; өөрчлөгдсөн нь зөвхөн гарын үсгийн дараах шалгалт.
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

-- ─────────────────────────────────────────────────────────────────────────
-- №1 — хөдөлгөөнийг хүлээн авагч хаана
-- ─────────────────────────────────────────────────────────────────────────

-- Объект дуудагчийн идэвхтэй тенантынх уу. Бодлого ба функц хоёулаа асуудаг
-- тул нэг газар.
-- +goose StatementBegin
CREATE FUNCTION petro_owns_site(kind TEXT, site UUID) RETURNS BOOLEAN
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = workspace, registry, pg_catalog, pg_temp
AS $$
    SELECT CASE kind
        WHEN 'station' THEN EXISTS (
            SELECT 1 FROM petro_stations s
             WHERE s.id = site
               AND s.tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid)
        WHEN 'depot' THEN EXISTS (
            SELECT 1 FROM petro_depots d
             WHERE d.id = site
               AND d.tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid)
        ELSE FALSE
    END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION petro_owns_site(TEXT, UUID) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION petro_owns_site(TEXT, UUID) TO gerege_nexus_tenant;

-- Хүлээн авагч өөр рүүгээ ирж буй хөдөлгөөнийг харна. Зөвхөн SELECT: хаах нь
-- доорх функцээр. Харахгүй бол 15 °C-ийн засвар хийх бүтээгдэхүүнээ ч мэдэхгүй.
CREATE POLICY receiver_read ON petro_movements FOR SELECT TO gerege_nexus_tenant
    USING (petro_owns_site(to_kind, to_id));

-- 15 °C-ийн засвар (volume.go) Go-д бодогдоно; зөрүү энд, мэдүүлсэн тоотой нэг
-- түгжээн дотор. ±9999-ийн хязгаар нь баганын NUMERIC(8,4) — нэг илүү тэг нь
-- overflow болж хөдөлгөөнийг мөнхөд нээлттэй үлдээдэг байв (аудит §13).
-- +goose StatementBegin
CREATE FUNCTION petro_close_movement(movement UUID, received NUMERIC, received_15c NUMERIC,
                                     closer UUID, closing_note TEXT)
RETURNS SETOF petro_movements
LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path = workspace, registry, pg_catalog, pg_temp
AS $$
DECLARE
    mv petro_movements%ROWTYPE;
    base NUMERIC;
    got NUMERIC;
    gap NUMERIC;
BEGIN
    IF received IS NULL OR received < 0 OR received_15c < 0 THEN
        RAISE EXCEPTION 'received liters must not be negative' USING ERRCODE = 'check_violation';
    END IF;

    SELECT * INTO mv FROM petro_movements m WHERE m.id = movement FOR UPDATE;
    -- «Байхгүй» ба «танайх биш» нэг хариу: илгээгч ч, хяналт ч энд хаахгүй.
    IF NOT FOUND OR NOT petro_owns_site(mv.to_kind, mv.to_id) THEN
        RAISE EXCEPTION 'movement is not addressed to this organisation'
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF mv.status <> 'open' THEN
        RAISE EXCEPTION 'movement is %', mv.status USING ERRCODE = 'object_not_in_prerequisite_state';
    END IF;

    base := mv.declared_liters;
    got := received;
    IF mv.declared_liters_15c IS NOT NULL AND received_15c IS NOT NULL THEN
        base := mv.declared_liters_15c;
        got := received_15c;
    END IF;
    IF base > 0 THEN
        gap := LEAST(9999, GREATEST(-9999, (base - got) / base * 100));
    END IF;

    RETURN QUERY
    UPDATE petro_movements m
       SET received_liters = received, received_liters_15c = received_15c, variance_pct = gap,
           status = 'closed', closed_at = NOW(), closed_by = closer,
           note = CASE WHEN COALESCE(closing_note, '') = '' THEN m.note ELSE closing_note END
     WHERE m.id = movement
    RETURNING m.*;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION petro_close_movement(UUID, NUMERIC, NUMERIC, UUID, TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION petro_close_movement(UUID, NUMERIC, NUMERIC, UUID, TEXT) TO gerege_nexus_tenant;

-- Маргаан: зөвхөн төлөв ба тэмдэглэл. NULL буцаавал олдсонгүй эсвэл маргах
-- боломжгүй төлөвт.
-- +goose StatementBegin
CREATE FUNCTION petro_dispute_movement(movement UUID, dispute_note TEXT)
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

REVOKE ALL ON FUNCTION petro_dispute_movement(UUID, TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION petro_dispute_movement(UUID, TEXT) TO gerege_nexus_tenant;

DROP POLICY IF EXISTS oversight_close ON petro_movements;
-- Хөдөлгөөнийг өөрчлөх зам нь дээрх хоёр функц л. Эрх үлдвэл илгээгч
-- `tenant_isolation`-оор өөрийн мөрөө шууд хаах боломжтой хэвээр.
REVOKE UPDATE ON petro_movements FROM gerege_nexus_tenant;

-- ─────────────────────────────────────────────────────────────────────────
-- №2 — өөр компанийн ШТС рүү ачилт ба хүлээн авалт
-- ─────────────────────────────────────────────────────────────────────────

-- Ачигч өөр компанийн ШТС-ийг RLS-ээр хардаггүй. ШТС-ийн жагсаалт нийтэд
-- нээлттэй (газрын зураг) тул бүртгэлийн төлөвийг хэлэх нь шинэ зүйл задлахгүй.
-- NULL — ийм ШТС байхгүй.
-- +goose StatementBegin
CREATE FUNCTION petro_station_registry_status(station UUID) RETURNS TEXT
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = workspace, registry, pg_catalog, pg_temp
AS $$
    SELECT registry_status FROM petro_stations WHERE id = station;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION petro_station_registry_status(UUID) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION petro_station_registry_status(UUID) TO gerege_nexus_tenant;

-- Рейс нь ачигчийн, партийн мөр нь импортлогчийн, ШТС-ийн нөөц ба хүлээн
-- авалт нь ШТС-ийн тенантынх. Хүлээн авагчийн сессээр гурвууланд нь хүрэх
-- цорын ганц зам. Хоёр дахь хүлээн авалтыг `idx_receipts_one_per_trip` 23505-аар
-- татгалзана; хэтэрсэн савыг `station_stock_within_capacity` 23514-өөр.
-- +goose StatementBegin
CREATE FUNCTION petro_receive_trip(trip UUID, received NUMERIC, manifest NUMERIC,
                                   seal TEXT, receiver UUID, receipt_note TEXT)
RETURNS TABLE (receipt_id UUID, receipt_at TIMESTAMPTZ, receipt_station UUID,
               receipt_station_name TEXT, trip_fuel TEXT, trip_fuel_label TEXT,
               trip_batch UUID, trip_batch_code TEXT, stock_after NUMERIC)
LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path = workspace, registry, pg_catalog, pg_temp
AS $$
DECLARE
    caller UUID := NULLIF(current_setting('app.current_tenant', true), '')::uuid;
    t petro_dispatch_trips%ROWTYPE;
BEGIN
    IF caller IS NULL THEN
        RAISE EXCEPTION 'no organisation in scope' USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF received IS NULL OR received <= 0 THEN
        RAISE EXCEPTION 'received liters must be positive' USING ERRCODE = 'check_violation';
    END IF;
    IF seal NOT IN ('sealed_intact', 'opened_authorized', 'seal_tampered') THEN
        RAISE EXCEPTION 'unknown seal status %', seal USING ERRCODE = 'check_violation';
    END IF;

    SELECT * INTO t FROM petro_dispatch_trips d WHERE d.id = trip FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'trip is not addressed to this organisation'
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    -- ШТС нь замд байхдаа устгагдсан (ON DELETE SET NULL). Ачигч л үүнийг
    -- мэдэх эрхтэй — бусдад «олдсонгүй».
    IF t.to_station_id IS NULL THEN
        IF t.tenant_id = caller THEN
            RAISE EXCEPTION 'trip names no station' USING ERRCODE = 'object_not_in_prerequisite_state';
        END IF;
        RAISE EXCEPTION 'trip is not addressed to this organisation'
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF NOT petro_owns_site('station', t.to_station_id) THEN
        RAISE EXCEPTION 'trip is not addressed to this organisation'
            USING ERRCODE = 'insufficient_privilege';
    END IF;

    receipt_station := t.to_station_id;
    trip_fuel := t.fuel_type;
    trip_fuel_label := t.fuel_label;
    trip_batch := t.batch_id;

    INSERT INTO petro_station_receipts
           (tenant_id, station_id, trip_id, batch_id, fuel_type, liters,
            seal_status, manifest_liters, received_by, note)
    VALUES (caller, t.to_station_id, t.id, t.batch_id, t.fuel_type, received,
            seal, manifest, receiver, COALESCE(receipt_note, ''))
    RETURNING id, received_at INTO receipt_id, receipt_at;

    INSERT INTO petro_station_inventory AS inv
           (station_id, tenant_id, fuel_type, fuel_label, price_mnt,
            current_stock_liters, last_reported_at)
    VALUES (t.to_station_id, caller, t.fuel_type, t.fuel_label, 0, received, NOW())
    ON CONFLICT (station_id, fuel_type) DO UPDATE
       SET current_stock_liters = inv.current_stock_liters + received,
           last_reported_at = NOW()
    RETURNING inv.current_stock_liters INTO stock_after;

    UPDATE petro_dispatch_trips d
       SET status = 'completed', completed_at = NOW(), updated_at = NOW()
     WHERE d.id = t.id AND d.completed_at IS NULL;

    IF t.batch_id IS NOT NULL THEN
        UPDATE petro_batches b
           SET received_liters = b.received_liters + received, updated_at = NOW()
         WHERE b.id = t.batch_id
        RETURNING b.batch_code INTO trip_batch_code;
    END IF;

    SELECT s.name INTO receipt_station_name FROM petro_stations s WHERE s.id = t.to_station_id;

    RETURN NEXT;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION petro_receive_trip(UUID, NUMERIC, NUMERIC, TEXT, UUID, TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION petro_receive_trip(UUID, NUMERIC, NUMERIC, TEXT, UUID, TEXT) TO gerege_nexus_tenant;

-- ─────────────────────────────────────────────────────────────────────────
-- №3 — тайланг хянах нь төлөв ба хяналтын багана л
-- ─────────────────────────────────────────────────────────────────────────
--
-- Дөрвөн нүд ба «зөвхөн submitted» нь WHERE-д: илгээгч өөрөө эсвэл аль хэдийн
-- шийдэгдсэн бол 0 мөр — handler 409. Handler эдгээрийг урьдчилан уншиж зөв
-- мессеж өгдөг; энд байгаа нь уралдааны хамгаалалт.
-- +goose StatementBegin
CREATE FUNCTION petro_review_submission(submission UUID, decision TEXT, verdict_note TEXT,
                                        reviewer UUID)
RETURNS SETOF petro_report_submissions
LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path = workspace, registry, pg_catalog, pg_temp
AS $$
BEGIN
    IF petro_oversight_scope() IS DISTINCT FROM 'national' THEN
        RAISE EXCEPTION 'not a national oversight body' USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF decision NOT IN ('approved', 'returned') THEN
        RAISE EXCEPTION 'unknown decision %', decision USING ERRCODE = 'check_violation';
    END IF;
    IF decision = 'returned' AND COALESCE(verdict_note, '') = '' THEN
        RAISE EXCEPTION 'a return needs a reason' USING ERRCODE = 'check_violation';
    END IF;
    IF reviewer IS NULL THEN
        RAISE EXCEPTION 'no reviewer' USING ERRCODE = 'check_violation';
    END IF;

    RETURN QUERY
    UPDATE petro_report_submissions s
       SET status = decision, reviewed_by = reviewer, reviewed_at = NOW(),
           review_note = COALESCE(verdict_note, '')
     WHERE s.id = submission
       AND s.status = 'submitted'
       AND s.submitted_by IS DISTINCT FROM reviewer
    RETURNING s.*;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION petro_review_submission(UUID, TEXT, TEXT, UUID) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION petro_review_submission(UUID, TEXT, TEXT, UUID) TO gerege_nexus_tenant;

DROP POLICY IF EXISTS oversight_review ON petro_report_submissions;
-- Ирүүлэлтийг өөрчлөх Go код байхгүй (review.go л байсан). Эрх үлдвэл компани
-- өөрийн хэш гинжийг `tenant_isolation`-оор дарж бичих боломжтой.
REVOKE UPDATE ON petro_report_submissions FROM gerege_nexus_tenant;
-- Мөр ба дүгнэлтийг устгадаг Go код байхгүй; устгал нь ирүүлэлтийн CASCADE-аар л.
REVOKE DELETE ON petro_report_lines FROM gerege_nexus_tenant;
REVOKE DELETE ON petro_validation_findings FROM gerege_nexus_tenant;

-- ─────────────────────────────────────────────────────────────────────────
-- №5 — ваучерыг QR кодоор л хаана
-- ─────────────────────────────────────────────────────────────────────────

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION petro_redeem_voucher(voucher UUID, qr TEXT, station UUID, liters NUMERIC)
RETURNS TABLE (voucher_id UUID, voucher_amount NUMERIC, voucher_fuel TEXT)
LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path = workspace, registry, pg_catalog, pg_temp
AS $$
DECLARE
    caller UUID := NULLIF(current_setting('app.current_tenant', true), '')::uuid;
BEGIN
    IF caller IS NULL THEN
        RAISE EXCEPTION 'no organisation in scope'
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    -- UUID нь аудитын логт бичигддэг тул нууц биш. Иргэний үзүүлсэн код л
    -- ваучерыг хаана; id нь өгөгдвөл нэмэлт тулгалт.
    IF qr IS NULL OR qr = '' THEN
        RAISE EXCEPTION 'the voucher code is required'
            USING ERRCODE = 'check_violation';
    END IF;
    IF liters IS NULL OR liters <= 0 THEN
        RAISE EXCEPTION 'liters must be positive'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM petro_stations s
         WHERE s.id = station AND s.tenant_id = caller
    ) THEN
        RAISE EXCEPTION 'station is not this operator''s'
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM petro_stations s
         WHERE s.id = station
           AND s.registry_status = 'active' AND s.is_voucher_enabled
    ) THEN
        RAISE EXCEPTION 'station is suspended or does not take vouchers'
            USING ERRCODE = 'object_not_in_prerequisite_state';
    END IF;

    RETURN QUERY
    UPDATE petro_vouchers v
       SET status = 'redeemed',
           redeemed_at = NOW(),
           redeemed_station_id = station,
           redeemed_liters = liters
     WHERE v.qr_token = qr
       AND (voucher IS NULL OR v.id = voucher)
       AND v.status = 'active'
       AND v.expires_at > NOW()
    RETURNING v.id, v.amount_mnt, v.fuel_type;
END;
$$;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION petro_redeem_voucher(voucher UUID, qr TEXT, station UUID, liters NUMERIC)
RETURNS TABLE (voucher_id UUID, voucher_amount NUMERIC, voucher_fuel TEXT)
LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path = workspace, registry, pg_catalog, pg_temp
AS $$
DECLARE
    caller UUID := NULLIF(current_setting('app.current_tenant', true), '')::uuid;
BEGIN
    IF caller IS NULL THEN
        RAISE EXCEPTION 'no organisation in scope'
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF voucher IS NULL AND qr IS NULL THEN
        RAISE EXCEPTION 'neither a voucher id nor a code'
            USING ERRCODE = 'check_violation';
    END IF;
    IF liters IS NULL OR liters <= 0 THEN
        RAISE EXCEPTION 'liters must be positive'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM petro_stations s
         WHERE s.id = station AND s.tenant_id = caller
    ) THEN
        RAISE EXCEPTION 'station is not this operator''s'
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM petro_stations s
         WHERE s.id = station
           AND s.registry_status = 'active' AND s.is_voucher_enabled
    ) THEN
        RAISE EXCEPTION 'station is suspended or does not take vouchers'
            USING ERRCODE = 'object_not_in_prerequisite_state';
    END IF;

    RETURN QUERY
    UPDATE petro_vouchers v
       SET status = 'redeemed',
           redeemed_at = NOW(),
           redeemed_station_id = station,
           redeemed_liters = liters
     WHERE (v.id = voucher OR v.qr_token = qr)
       AND v.status = 'active'
       AND v.expires_at > NOW()
    RETURNING v.id, v.amount_mnt, v.fuel_type;
END;
$$;
-- +goose StatementEnd

GRANT DELETE ON petro_validation_findings TO gerege_nexus_tenant;
GRANT DELETE ON petro_report_lines TO gerege_nexus_tenant;
GRANT UPDATE ON petro_report_submissions TO gerege_nexus_tenant;
CREATE POLICY oversight_review ON petro_report_submissions FOR UPDATE TO gerege_nexus_tenant
    USING (petro_is_oversight()) WITH CHECK (petro_is_oversight());
DROP FUNCTION IF EXISTS petro_review_submission(UUID, TEXT, TEXT, UUID);

DROP FUNCTION IF EXISTS petro_receive_trip(UUID, NUMERIC, NUMERIC, TEXT, UUID, TEXT);
DROP FUNCTION IF EXISTS petro_station_registry_status(UUID);

GRANT UPDATE ON petro_movements TO gerege_nexus_tenant;
CREATE POLICY oversight_close ON petro_movements FOR UPDATE TO gerege_nexus_tenant
    USING (petro_is_oversight()) WITH CHECK (petro_is_oversight());
DROP FUNCTION IF EXISTS petro_dispute_movement(UUID, TEXT);
DROP FUNCTION IF EXISTS petro_close_movement(UUID, NUMERIC, NUMERIC, UUID, TEXT);
DROP POLICY IF EXISTS receiver_read ON petro_movements;
DROP FUNCTION IF EXISTS petro_owns_site(TEXT, UUID);

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
       AND NOT petro_is_oversight() THEN
        RAISE EXCEPTION 'not an oversight body' USING ERRCODE = 'insufficient_privilege';
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION petro_set_site_status(site_kind TEXT, site UUID, new_status TEXT)
RETURNS TEXT
LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path = workspace, registry, pg_catalog, pg_temp
AS $$
DECLARE
    site_name TEXT;
BEGIN
    IF NOT petro_is_oversight() THEN
        RAISE EXCEPTION 'not an oversight body' USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF new_status NOT IN ('active', 'suspended', 'closed') THEN
        RAISE EXCEPTION 'unknown status %', new_status USING ERRCODE = 'check_violation';
    END IF;

    IF site_kind = 'station' THEN
        UPDATE petro_stations SET registry_status = new_status, updated_at = NOW()
         WHERE id = site RETURNING name INTO site_name;
    ELSIF site_kind = 'depot' THEN
        UPDATE petro_depots SET registry_status = new_status, updated_at = NOW()
         WHERE id = site RETURNING name INTO site_name;
    ELSE
        RAISE EXCEPTION 'unknown site kind %', site_kind USING ERRCODE = 'check_violation';
    END IF;

    RETURN site_name;
END;
$$;
-- +goose StatementEnd

DROP FUNCTION IF EXISTS petro_oversight_scope();
