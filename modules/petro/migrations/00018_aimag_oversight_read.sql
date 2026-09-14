-- Аймгийн хяналтын байгууллага зөвхөн өөрийн аймгийг харна (2026-09-14).
--
-- # Юу буруу байсан бэ
--
-- 00016 бичих эрхийг `national`-д хязгаарласан ч унших бодлого бүр
-- `petro_is_oversight()` хэвээр: `scope = 'aimag'` байгууллага улсын бүх ШТС,
-- бааз, тайлан, хөдөлгөөнийг хардаг байв. `petro_oversight_bodies.aimag`
-- хэзээ ч уншигдаагүй.
--
-- # Загвар
--
-- Аймгийн бус хүрээ (national, tax, customs, audit) өмнөх шигээ бүгдийг харна.
-- Аймгийн байгууллага нь тухайн аймагт бүртгэлтэй ШТС/баазын id-гаар шүүгдэнэ.
-- Функцууд `(SELECT …)` дотор дуудагдах тул асуулгад нэг удаа бодогдоно
-- (InitPlan) — мөр бүрд SECURITY DEFINER дуудахгүй. Аймгийг `lower(btrim())`
-- -ээр тулгана: нэрийг компани, оператор хоёр тусдаа бичдэг.
--
-- Дүгнэлт (finding) нь мөрөөрөө шүүгдэнэ: нэг ирүүлэлт олон аймгийн мөртэй
-- байж болох ба finding бүр `detail`-дээ ШТС-ийн id-г бичдэг. Мөргүй
-- (ирүүлэлтийн түвшний) finding-ийг аймгийн байгууллага харахгүй. Ирүүлэлтийн
-- мөр өөрөө (row_count, error_count) компанийн нийт тоог харуулсаар.
--
-- Байршилгүй хүснэгт: `petro_customs_shipments` (хилийн боомт аймагт
-- холбогдоогүй) — аймгийн байгууллага харахгүй. Аймаггүй аймгийн байгууллага
-- юу ч харахгүй (registry.go томилохдоо татгалзана).

-- +goose Up

-- +goose StatementBegin
CREATE FUNCTION petro_oversight_sees_all() RETURNS BOOLEAN
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = workspace, registry, pg_catalog, pg_temp
AS $$
    SELECT EXISTS (
        SELECT 1 FROM petro_oversight_bodies
         WHERE tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid
           AND scope <> 'aimag');
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION petro_oversight_aimag() RETURNS TEXT
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = workspace, registry, pg_catalog, pg_temp
AS $$
    SELECT lower(btrim(aimag)) FROM petro_oversight_bodies
     WHERE tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid
       AND scope = 'aimag'
       AND btrim(aimag) <> '';
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION petro_oversight_sites() RETURNS UUID[]
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = workspace, registry, pg_catalog, pg_temp
AS $$
    SELECT COALESCE(array_agg(site.id), '{}'::uuid[])
      FROM (SELECT s.id FROM petro_stations s
             WHERE lower(btrim(s.aimag)) = petro_oversight_aimag()
            UNION ALL
            SELECT d.id FROM petro_depots d
             WHERE lower(btrim(d.aimag)) = petro_oversight_aimag()) site;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION petro_oversight_sees_all() FROM PUBLIC;
REVOKE ALL ON FUNCTION petro_oversight_aimag() FROM PUBLIC;
REVOKE ALL ON FUNCTION petro_oversight_sites() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION petro_oversight_sees_all() TO gerege_nexus_tenant;
GRANT EXECUTE ON FUNCTION petro_oversight_aimag() TO gerege_nexus_tenant;
GRANT EXECUTE ON FUNCTION petro_oversight_sites() TO gerege_nexus_tenant;

DROP POLICY oversight_read ON petro_stations;
CREATE POLICY oversight_read ON petro_stations FOR SELECT TO gerege_nexus_tenant
    USING ((SELECT petro_oversight_sees_all())
           OR id = ANY ((SELECT petro_oversight_sites())));

DROP POLICY oversight_read ON petro_station_inventory;
CREATE POLICY oversight_read ON petro_station_inventory FOR SELECT TO gerege_nexus_tenant
    USING ((SELECT petro_oversight_sees_all())
           OR station_id = ANY ((SELECT petro_oversight_sites())));

DROP POLICY oversight_read ON petro_depots;
CREATE POLICY oversight_read ON petro_depots FOR SELECT TO gerege_nexus_tenant
    USING ((SELECT petro_oversight_sees_all())
           OR id = ANY ((SELECT petro_oversight_sites())));

DROP POLICY oversight_read ON petro_depot_tanks;
CREATE POLICY oversight_read ON petro_depot_tanks FOR SELECT TO gerege_nexus_tenant
    USING ((SELECT petro_oversight_sees_all())
           OR depot_id = ANY ((SELECT petro_oversight_sites())));

DROP POLICY oversight_read ON petro_customs_shipments;
CREATE POLICY oversight_read ON petro_customs_shipments FOR SELECT TO gerege_nexus_tenant
    USING ((SELECT petro_oversight_sees_all()));

DROP POLICY oversight_read ON petro_dispatch_trips;
CREATE POLICY oversight_read ON petro_dispatch_trips FOR SELECT TO gerege_nexus_tenant
    USING ((SELECT petro_oversight_sees_all())
           OR from_depot_id = ANY ((SELECT petro_oversight_sites()))
           OR to_station_id = ANY ((SELECT petro_oversight_sites())));

DROP POLICY oversight_read ON petro_report_submissions;
CREATE POLICY oversight_read ON petro_report_submissions FOR SELECT TO gerege_nexus_tenant
    USING ((SELECT petro_oversight_sees_all())
           OR EXISTS (SELECT 1 FROM petro_report_lines l
                       WHERE l.submission_id = petro_report_submissions.id
                         AND l.site_id = ANY ((SELECT petro_oversight_sites()))));

DROP POLICY oversight_read ON petro_report_lines;
CREATE POLICY oversight_read ON petro_report_lines FOR SELECT TO gerege_nexus_tenant
    USING ((SELECT petro_oversight_sees_all())
           OR site_id = ANY ((SELECT petro_oversight_sites())));

DROP POLICY oversight_read ON petro_validation_findings;
CREATE POLICY oversight_read ON petro_validation_findings FOR SELECT TO gerege_nexus_tenant
    USING ((SELECT petro_oversight_sees_all())
           OR line_id IN (SELECT l.id FROM petro_report_lines l
                           WHERE l.site_id = ANY ((SELECT petro_oversight_sites()))));

DROP POLICY oversight_read ON petro_movements;
CREATE POLICY oversight_read ON petro_movements FOR SELECT TO gerege_nexus_tenant
    USING ((SELECT petro_oversight_sees_all())
           OR from_id = ANY ((SELECT petro_oversight_sites()))
           OR to_id = ANY ((SELECT petro_oversight_sites())));

DROP POLICY oversight_read ON petro_devices;
CREATE POLICY oversight_read ON petro_devices FOR SELECT TO gerege_nexus_tenant
    USING ((SELECT petro_oversight_sees_all())
           OR site_id = ANY ((SELECT petro_oversight_sites())));

DROP POLICY oversight_read ON petro_daily_national;
CREATE POLICY oversight_read ON petro_daily_national FOR SELECT TO gerege_nexus_tenant
    USING ((SELECT petro_oversight_sees_all())
           OR lower(btrim(aimag)) = (SELECT petro_oversight_aimag()));

-- +goose Down

DROP POLICY oversight_read ON petro_daily_national;
CREATE POLICY oversight_read ON petro_daily_national FOR SELECT TO gerege_nexus_tenant
    USING (petro_is_oversight());
DROP POLICY oversight_read ON petro_devices;
CREATE POLICY oversight_read ON petro_devices FOR SELECT TO gerege_nexus_tenant
    USING (petro_is_oversight());
DROP POLICY oversight_read ON petro_movements;
CREATE POLICY oversight_read ON petro_movements FOR SELECT TO gerege_nexus_tenant
    USING (petro_is_oversight());
DROP POLICY oversight_read ON petro_validation_findings;
CREATE POLICY oversight_read ON petro_validation_findings FOR SELECT TO gerege_nexus_tenant
    USING (petro_is_oversight());
DROP POLICY oversight_read ON petro_report_lines;
CREATE POLICY oversight_read ON petro_report_lines FOR SELECT TO gerege_nexus_tenant
    USING (petro_is_oversight());
DROP POLICY oversight_read ON petro_report_submissions;
CREATE POLICY oversight_read ON petro_report_submissions FOR SELECT TO gerege_nexus_tenant
    USING (petro_is_oversight());
DROP POLICY oversight_read ON petro_dispatch_trips;
CREATE POLICY oversight_read ON petro_dispatch_trips FOR SELECT TO gerege_nexus_tenant
    USING (petro_is_oversight());
DROP POLICY oversight_read ON petro_customs_shipments;
CREATE POLICY oversight_read ON petro_customs_shipments FOR SELECT TO gerege_nexus_tenant
    USING (petro_is_oversight());
DROP POLICY oversight_read ON petro_depot_tanks;
CREATE POLICY oversight_read ON petro_depot_tanks FOR SELECT TO gerege_nexus_tenant
    USING (petro_is_oversight());
DROP POLICY oversight_read ON petro_depots;
CREATE POLICY oversight_read ON petro_depots FOR SELECT TO gerege_nexus_tenant
    USING (petro_is_oversight());
DROP POLICY oversight_read ON petro_station_inventory;
CREATE POLICY oversight_read ON petro_station_inventory FOR SELECT TO gerege_nexus_tenant
    USING (petro_is_oversight());
DROP POLICY oversight_read ON petro_stations;
CREATE POLICY oversight_read ON petro_stations FOR SELECT TO gerege_nexus_tenant
    USING (petro_is_oversight());

DROP FUNCTION IF EXISTS petro_oversight_sites();
DROP FUNCTION IF EXISTS petro_oversight_aimag();
DROP FUNCTION IF EXISTS petro_oversight_sees_all();
