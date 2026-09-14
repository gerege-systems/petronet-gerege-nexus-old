-- Аудитын дараах засварууд (2026-09-14).
--
-- # SECURITY DEFINER функцүүдийн search_path
--
-- 00009, 00011, 00014-ийн дөрвөн функц эзэмшигчийн (superuser) эрхээр
-- ажилладаг атлаа search_path тавиагүй тул дуудагчийнхыг өвлөдөг. Postgres
-- `pg_temp`-ийг далд байдлаар хамгийн түрүүнд хайдаг: тенантын дүрээр SQL
-- ажиллуулж чадсан хэн ч `CREATE TEMP TABLE petro_oversight_bodies` хийж
-- `petro_is_oversight()`-ийг өөртөө true болгох боломжтой байв.
--
-- 00014-ийн тайлбар «`pg_catalog, public` гэвэл хүснэгт олдохгүй» гэдэг нь
-- зөв — хариулт нь search_path-гүй орхих биш, модулийн schema-г нэрлэж
-- `pg_temp`-ийг сүүлд тавих. Хүснэгтүүд `workspace`-д (цөмийн 00084 `tenant`-ийг
-- сольсон), `registry.tenants` нь өөрийн schema-тай.
--
-- # Бусад
--
-- * `petro_depots` дээр DELETE эрх байгаагүй тул «бааз устгах» үргэлж 500.
-- * ШТС-ийн үнэ, багтаамж сөрөг байж болдог байв: `tank_capacity_liters <= 0`
--   нь 00012-д «хязгааргүй» гэсэн утгатай тул -1 илгээж дээд хязгаарыг
--   тойрох боломжтой. 0 нь «хязгааргүй» хэвээр.
-- * Ваучер хаах функц литр > 0, ШТС идэвхтэй (`registry_status`) ба ваучер
--   хүлээн авдаг (`is_voucher_enabled`) эсэхийг шалгадаггүй байв: хяналтын
--   түдгэлзүүлсэн ШТС ваучер хаасаар байв.
-- * `petro_vouchers` хамгийн хурдан өсдөг хүснэгт атлаа метрикийн асуулга
--   (`for_date`) ба ШТС устгах үеийн `ON DELETE SET NULL` индексгүй байв.

-- +goose Up

ALTER FUNCTION petro_is_oversight()
    SET search_path = workspace, registry, pg_catalog, pg_temp;
ALTER FUNCTION petro_set_site_status(TEXT, UUID, TEXT)
    SET search_path = workspace, registry, pg_catalog, pg_temp;
ALTER FUNCTION petro_refresh_daily(DATE)
    SET search_path = workspace, registry, pg_catalog, pg_temp;

GRANT DELETE ON petro_depots TO gerege_nexus_tenant;

ALTER TABLE petro_station_inventory
    ADD CONSTRAINT station_inventory_price_nonnegative CHECK (price_mnt >= 0),
    ADD CONSTRAINT station_inventory_capacity_nonnegative CHECK (tank_capacity_liters >= 0);

CREATE INDEX IF NOT EXISTS idx_petro_vouchers_for_date_status ON petro_vouchers (for_date, status);
CREATE INDEX IF NOT EXISTS idx_petro_vouchers_intended_station ON petro_vouchers (intended_station_id);
CREATE INDEX IF NOT EXISTS idx_petro_vouchers_redeemed_station ON petro_vouchers (redeemed_station_id);

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
    -- Тенантын дүр энэ хүснэгтийг бодлогоор шүүдэг ч энэ функц эзэмшигчийн
    -- эрхээр ажиллаж байгаа тул шалгуурыг өөрөө бичнэ.
    IF NOT EXISTS (
        SELECT 1 FROM petro_stations s
         WHERE s.id = station AND s.tenant_id = caller
    ) THEN
        RAISE EXCEPTION 'station is not this operator''s'
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    -- Өөрийнх нь ШТС тул «олдсонгүй» гэж хариулах шалтгаан алга: тусдаа код,
    -- handler 409 болгоно.
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

-- +goose Down

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION petro_redeem_voucher(voucher UUID, qr TEXT, station UUID, liters NUMERIC)
RETURNS TABLE (voucher_id UUID, voucher_amount NUMERIC, voucher_fuel TEXT)
LANGUAGE plpgsql VOLATILE SECURITY DEFINER AS $$
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
    IF NOT EXISTS (
        SELECT 1 FROM petro_stations s
         WHERE s.id = station AND s.tenant_id = caller
    ) THEN
        RAISE EXCEPTION 'station is not this operator''s'
            USING ERRCODE = 'insufficient_privilege';
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
ALTER FUNCTION petro_redeem_voucher(UUID, TEXT, UUID, NUMERIC) RESET search_path;

DROP INDEX IF EXISTS idx_petro_vouchers_redeemed_station;
DROP INDEX IF EXISTS idx_petro_vouchers_intended_station;
DROP INDEX IF EXISTS idx_petro_vouchers_for_date_status;

ALTER TABLE petro_station_inventory
    DROP CONSTRAINT IF EXISTS station_inventory_capacity_nonnegative,
    DROP CONSTRAINT IF EXISTS station_inventory_price_nonnegative;

REVOKE DELETE ON petro_depots FROM gerege_nexus_tenant;

ALTER FUNCTION petro_refresh_daily(DATE) RESET search_path;
ALTER FUNCTION petro_set_site_status(TEXT, UUID, TEXT) RESET search_path;
ALTER FUNCTION petro_is_oversight() RESET search_path;
