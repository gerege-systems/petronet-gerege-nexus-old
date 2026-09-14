"use client";

/**
 * Depots — the tanks, and the one act that fills them.
 *
 * There is no field on this screen that sets a tank's level, and that absence
 * is the design. Litres in a vessel are the sum of what went in and what came
 * out; a box that could overwrite that sum would make every receipt beneath it
 * advisory, which is the state benzin-gerege-mn's stock figures were already
 * in — a number seeded at import and never moved by anything that happened.
 *
 * So the only way a level rises here is a consignment being unloaded, and the
 * form for it asks for two figures: what the delivery note claims and what the
 * gauge measured. Neither is reconciled against the other. A gap is loss,
 * theft, or a badly calibrated meter, and nothing on this screen can tell
 * which — but a screen that recorded one figure could not say a gap existed.
 */

import { useCallback, useEffect, useMemo, useState } from "react";
import { Droplets, Loader2, Plus, Train, Warehouse } from "lucide-react";

import { api, type Depot, type Shipment, type Tank } from "@/lib/api";
import { useI18n } from "@/lib/i18n";
import { AIMAGS } from "@/lib/petro/aimags";
import {
  ErrorNote,
  Field,
  FillBar,
  GhostButton,
  PrimaryButton,
  inputClass,
  litres,
} from "@/components/petro/operatorUI";

const GRADES = [
  { code: "ai92", label: "АИ-92" },
  { code: "ai95", label: "АИ-95" },
  { code: "ai98", label: "АИ-98" },
  { code: "ai80", label: "АИ-80" },
  { code: "diesel", label: "Дизель (ДТ)" },
];

export default function DepotsPage() {
  const { t } = useI18n();
  const [depots, setDepots] = useState<Depot[] | null>(null);
  const [shipments, setShipments] = useState<Shipment[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [addingTankTo, setAddingTankTo] = useState<string | null>(null);
  const [receivingAt, setReceivingAt] = useState<Depot | null>(null);

  const load = useCallback(() => {
    Promise.all([api.listFuelDepots(), api.listFuelShipments()])
      .then(([depotList, shipmentList]) => {
        setDepots(depotList.depots);
        setShipments(shipmentList.shipments);
      })
      .catch((err) => setError(err instanceof Error ? err.message : String(err)));
  }, []);

  useEffect(load, [load]);

  // What may still be unloaded. A consignment already at a depot is done, and
  // one that customs has not released must not appear as an option — the
  // handler refuses it, and offering it would be an invitation to be refused.
  const unloadable = useMemo(
    () => shipments.filter((s) => s.status === "cleared" || s.status === "in_transit"),
    [shipments],
  );

  return (
    <div>
      <div className="mb-6 flex items-start justify-between gap-6">
        <p className="max-w-2xl text-slate-500">{t("petro.depot.subtitle")}</p>
        <PrimaryButton onClick={() => setAdding(true)} className="shrink-0">
          <Plus className="h-4 w-4" />
          {t("petro.depot.add")}
        </PrimaryButton>
      </div>

      {error ? <ErrorNote>{error}</ErrorNote> : null}

      {adding ? (
        <DepotForm
          onCancel={() => setAdding(false)}
          onDone={() => {
            setAdding(false);
            load();
          }}
        />
      ) : null}

      {depots === null ? (
        <Loader2 className="h-5 w-5 animate-spin text-slate-400" />
      ) : depots.length === 0 ? (
        <p className="rounded-2xl border border-slate-200 bg-white p-10 text-center text-sm text-slate-500">
          {t("petro.depot.empty")}
        </p>
      ) : (
        <div className="grid gap-4">
          {depots.map((depot) => (
            <DepotCard
              key={depot.id}
              depot={depot}
              onAddTank={() => setAddingTankTo(depot.id)}
              onReceive={() => setReceivingAt(depot)}
              canReceive={unloadable.length > 0 && (depot.tanks?.length ?? 0) > 0}
            />
          ))}
        </div>
      )}

      {addingTankTo ? (
        <TankDialog
          depotId={addingTankTo}
          onCancel={() => setAddingTankTo(null)}
          onDone={() => {
            setAddingTankTo(null);
            load();
          }}
        />
      ) : null}

      {receivingAt ? (
        <ReceiveDialog
          depot={receivingAt}
          shipments={unloadable}
          onCancel={() => setReceivingAt(null)}
          onDone={() => {
            setReceivingAt(null);
            load();
          }}
        />
      ) : null}
    </div>
  );
}

function DepotCard({
  depot,
  onAddTank,
  onReceive,
  canReceive,
}: {
  depot: Depot;
  onAddTank: () => void;
  onReceive: () => void;
  canReceive: boolean;
}) {
  const { t } = useI18n();
  const tanks = depot.tanks ?? [];

  return (
    <section className="rounded-2xl border border-slate-200 bg-white p-5">
      <header className="mb-4 flex flex-wrap items-start justify-between gap-4">
        <div>
          <h2 className="flex items-center gap-2 font-semibold text-slate-900">
            <Warehouse className="h-4 w-4 text-slate-400" />
            {depot.name}
            {depot.has_rail_siding ? (
              <span
                className="inline-flex items-center gap-1 rounded-full bg-slate-100 px-2 py-0.5 text-xs font-normal text-slate-600"
                title={t("petro.depot.rail")}
              >
                <Train className="h-3 w-3" />
                {depot.rail_station_code || t("petro.depot.rail")}
              </span>
            ) : null}
          </h2>
          <p className="mt-0.5 text-sm text-slate-500">
            {[depot.aimag, depot.district, depot.address].filter(Boolean).join(" · ") || "—"}
          </p>
        </div>

        <div className="flex items-center gap-2">
          <GhostButton onClick={onAddTank}>{t("petro.depot.add_tank")}</GhostButton>
          <PrimaryButton onClick={onReceive} disabled={!canReceive}>
            <Droplets className="h-4 w-4" />
            {t("petro.depot.receive")}
          </PrimaryButton>
        </div>
      </header>

      {tanks.length === 0 ? (
        <p className="text-sm text-slate-400">{t("petro.depot.no_tanks")}</p>
      ) : (
        <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {tanks.map((tank) => (
            <TankRow key={tank.id} tank={tank} />
          ))}
        </ul>
      )}
    </section>
  );
}

function TankRow({ tank }: { tank: Tank }) {
  const { t } = useI18n();
  return (
    <li className="rounded-xl border border-slate-200 p-3">
      <div className="mb-2 flex items-baseline justify-between gap-2">
        <span className="text-sm font-medium text-slate-900">{tank.tank_no}</span>
        <span className="text-xs text-slate-500">{tank.fuel_label || tank.fuel_type}</span>
      </div>
      <FillBar percent={tank.fill_percent} />
      <p className="mt-2 text-xs text-slate-500">
        <b className="tabular-nums text-slate-900">{litres(tank.current_liters)}</b> /{" "}
        <span className="tabular-nums">{litres(tank.capacity_liters)}</span> л ·{" "}
        {tank.fill_percent.toFixed(1)}% {t("petro.depot.of_capacity")}
      </p>
      {tank.temperature_c !== null || tank.density_kg_m3 !== null ? (
        <p className="mt-1 text-xs text-slate-400">
          {tank.temperature_c !== null ? `${tank.temperature_c}°C` : null}
          {tank.temperature_c !== null && tank.density_kg_m3 !== null ? " · " : null}
          {tank.density_kg_m3 !== null ? `${tank.density_kg_m3} кг/м³` : null}
        </p>
      ) : null}
    </li>
  );
}

function DepotForm({ onCancel, onDone }: { onCancel: () => void; onDone: () => void }) {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [form, setForm] = useState({
    name: "",
    aimag: "",
    district: "",
    address: "",
    rail_station_code: "",
    has_rail_siding: false,
  });

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.createFuelDepot({ ...form, name: form.name.trim() });
      onDone();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="mb-6 rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <Field label={t("petro.depot.name")}>
          <input
            required
            className={inputClass}
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
          />
        </Field>
        <Field label={t("petro.depot.aimag")}>
          <select
            className={inputClass}
            value={form.aimag}
            onChange={(e) => setForm({ ...form, aimag: e.target.value })}
          >
            <option value="">—</option>
            {AIMAGS.map((aimag) => (
              <option key={aimag}>{aimag}</option>
            ))}
          </select>
        </Field>
        <Field label={t("petro.depot.rail_code")}>
          <input
            className={inputClass}
            value={form.rail_station_code}
            onChange={(e) =>
              setForm({
                ...form,
                rail_station_code: e.target.value,
                // A siding code is what makes a base reachable by wagon, so
                // typing one is saying there is one.
                has_rail_siding: e.target.value.trim() !== "" || form.has_rail_siding,
              })
            }
          />
        </Field>
      </div>

      <label className="mt-4 flex items-center gap-2 text-sm text-slate-700">
        <input
          type="checkbox"
          checked={form.has_rail_siding}
          onChange={(e) => setForm({ ...form, has_rail_siding: e.target.checked })}
          className="h-4 w-4 rounded border-slate-300"
        />
        {t("petro.depot.rail")}
      </label>

      {error ? <p className="mt-4 text-sm text-red-600">{error}</p> : null}

      <div className="mt-5 flex gap-2">
        <PrimaryButton type="submit" busy={busy}>
          {t("petro.common.save")}
        </PrimaryButton>
        <GhostButton type="button" onClick={onCancel}>
          {t("petro.common.cancel")}
        </GhostButton>
      </div>
    </form>
  );
}

/** Adding a vessel. Capacity and grade — no opening level, by design. */
function TankDialog({
  depotId,
  onCancel,
  onDone,
}: {
  depotId: string;
  onCancel: () => void;
  onDone: () => void;
}) {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [form, setForm] = useState({ tank_no: "", fuel_type: GRADES[0].code, capacity_liters: "" });

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.createFuelTank(depotId, {
        tank_no: form.tank_no.trim(),
        fuel_type: form.fuel_type,
        capacity_liters: Number(form.capacity_liters),
      });
      onDone();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setBusy(false);
    }
  };

  return (
    <Dialog title={t("petro.depot.add_tank")} onCancel={onCancel}>
      <form onSubmit={submit}>
        <div className="grid gap-4">
          <Field label={t("petro.depot.tank_no")}>
            <input
              required
              className={inputClass}
              value={form.tank_no}
              onChange={(e) => setForm({ ...form, tank_no: e.target.value })}
              placeholder="Т-1"
            />
          </Field>
          <Field label={t("petro.ship.fuel_type")}>
            <select
              className={inputClass}
              value={form.fuel_type}
              onChange={(e) => setForm({ ...form, fuel_type: e.target.value })}
            >
              {GRADES.map((grade) => (
                <option key={grade.code} value={grade.code}>
                  {grade.label}
                </option>
              ))}
            </select>
          </Field>
          <Field label={t("petro.depot.capacity")}>
            <input
              required
              type="number"
              min="1"
              className={inputClass}
              value={form.capacity_liters}
              onChange={(e) => setForm({ ...form, capacity_liters: e.target.value })}
            />
          </Field>
        </div>

        {error ? <p className="mt-4 text-sm text-red-600">{error}</p> : null}

        <div className="mt-6 flex gap-2">
          <PrimaryButton type="submit" busy={busy}>
            {t("petro.common.save")}
          </PrimaryButton>
          <GhostButton type="button" onClick={onCancel} className="ml-auto">
            {t("petro.common.cancel")}
          </GhostButton>
        </div>
      </form>
    </Dialog>
  );
}

/**
 * Unloading a consignment into a tank.
 *
 * The tank list is filtered to vessels holding the consignment's grade: the
 * handler refuses a mismatch, and a diesel option under a petrol consignment is
 * an invitation to be refused rather than a choice.
 */
function ReceiveDialog({
  depot,
  shipments,
  onCancel,
  onDone,
}: {
  depot: Depot;
  shipments: Shipment[];
  onCancel: () => void;
  onDone: () => void;
}) {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [shipmentId, setShipmentId] = useState(shipments[0]?.id ?? "");
  const [tankId, setTankId] = useState("");
  const [liters, setLiters] = useState("");
  const [manifest, setManifest] = useState("");

  const shipment = shipments.find((s) => s.id === shipmentId);
  const tanks = useMemo(
    () => (depot.tanks ?? []).filter((tank) => tank.fuel_type === shipment?.fuel_type),
    [depot.tanks, shipment?.fuel_type],
  );

  // Follow the consignment: changing the grade changes which vessels are legal,
  // and a selection left pointing at one of the old ones would be refused on
  // submit for a reason the screen already knows.
  useEffect(() => {
    setTankId((current) => (tanks.some((tank) => tank.id === current) ? current : tanks[0]?.id ?? ""));
  }, [tanks]);

  useEffect(() => {
    if (shipment && liters === "") setLiters(String(Math.round(shipment.declared_liters)));
    // Only when the consignment changes: an attendant who has typed a measured
    // figure must not have it overwritten.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [shipmentId]);

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.receiveIntoFuelDepot(depot.id, {
        tank_id: tankId,
        shipment_id: shipmentId,
        liters: Number(liters),
        manifest_liters: manifest ? Number(manifest) : null,
      });
      onDone();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setBusy(false);
    }
  };

  return (
    <Dialog title={`${t("petro.depot.receive")} · ${depot.name}`} onCancel={onCancel}>
      <form onSubmit={submit}>
        <div className="grid gap-4">
          <Field label={t("petro.depot.which_shipment")}>
            <select
              className={inputClass}
              value={shipmentId}
              onChange={(e) => setShipmentId(e.target.value)}
            >
              {shipments.map((option) => (
                <option key={option.id} value={option.id}>
                  {option.declaration_no} · {option.fuel_label || option.fuel_type} ·{" "}
                  {litres(option.declared_liters)} л
                </option>
              ))}
            </select>
          </Field>

          {tanks.length === 0 ? (
            <p className="rounded-lg bg-amber-50 p-3 text-sm text-amber-800">
              {t("petro.depot.no_tanks")} — {shipment?.fuel_label || shipment?.fuel_type}
            </p>
          ) : (
            <Field label={t("petro.depot.receive_into")}>
              <select className={inputClass} value={tankId} onChange={(e) => setTankId(e.target.value)}>
                {tanks.map((tank) => (
                  <option key={tank.id} value={tank.id}>
                    {tank.tank_no} · {litres(tank.current_liters)} /{" "}
                    {litres(tank.capacity_liters)} л
                  </option>
                ))}
              </select>
            </Field>
          )}

          <div className="grid grid-cols-2 gap-4">
            <Field label={t("petro.depot.measured")}>
              <input
                required
                type="number"
                min="1"
                className={inputClass}
                value={liters}
                onChange={(e) => setLiters(e.target.value)}
              />
            </Field>
            <Field label={t("petro.depot.manifest")}>
              <input
                type="number"
                min="0"
                className={inputClass}
                value={manifest}
                onChange={(e) => setManifest(e.target.value)}
              />
            </Field>
          </div>
          <p className="text-xs text-slate-400">{t("petro.depot.manifest_note")}</p>
        </div>

        {error ? <p className="mt-4 text-sm text-red-600">{error}</p> : null}

        <div className="mt-6 flex gap-2">
          <PrimaryButton type="submit" busy={busy} disabled={!tankId}>
            {t("petro.common.save")}
          </PrimaryButton>
          <GhostButton type="button" onClick={onCancel} className="ml-auto">
            {t("petro.common.cancel")}
          </GhostButton>
        </div>
      </form>
    </Dialog>
  );
}

function Dialog({
  title,
  onCancel,
  children,
}: {
  title: string;
  onCancel: () => void;
  children: React.ReactNode;
}) {
  return (
    <div
      className="fixed inset-0 z-50 grid place-items-center bg-slate-900/40 p-4"
      onClick={(event) => event.target === event.currentTarget && onCancel()}
    >
      <div className="w-full max-w-md rounded-2xl bg-white p-6 shadow-xl">
        <h2 className="mb-5 text-lg font-semibold text-slate-900">{title}</h2>
        {children}
      </div>
    </div>
  );
}
