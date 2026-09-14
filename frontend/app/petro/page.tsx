"use client";

/**
 * ШТС — the register an operator keeps, rather than a list they watch.
 *
 * Until now this screen could only read: a station opened last week did not
 * exist here, a telephone number that changed stayed wrong, and a price — the
 * number a citizen's entitlement is spent against — was whatever cmd/petro-import
 * guessed months ago.
 *
 * # The one field this screen does not have
 *
 * A box for litres. It is the same rule the depot screen states: a level is the
 * sum of what deliveries put in the tank, and a field that overwrote that sum
 * would make every receipt underneath it advisory. So the grade row carries a
 * price, a vessel size and a status — and "we are out" is said with the status,
 * which is the sentence a citizen actually needs, rather than with a zero typed
 * into a box.
 */

import { useCallback, useEffect, useMemo, useState } from "react";
import { Fuel, Loader2, MapPin, Pencil, Phone, Plus, Ticket, Trash2 } from "lucide-react";

import { api, type FuelStation, type StationGrade } from "@/lib/api";
import { useI18n } from "@/lib/i18n";
import { AIMAGS } from "@/lib/petro/aimags";
import {
  ErrorNote,
  Field,
  GhostButton,
  PrimaryButton,
  inputClass,
  litres,
} from "@/components/petro/operatorUI";

// The grades cmd/petro-import already writes, spelled the way a Mongolian
// forecourt spells them. A suggestion the form offers, not a rule the server
// enforces — a new grade is a row, not a migration.
const GRADES = [
  { code: "ai92", label: "АИ-92" },
  { code: "ai95", label: "АИ-95" },
  { code: "ai98", label: "АИ-98" },
  { code: "ai80", label: "АИ-80" },
  { code: "diesel", label: "Дизель (ДТ)" },
  { code: "euro5_diesel", label: "Euro-5 ДТ" },
  { code: "lpg", label: "Газ (LPG)" },
];

const GRADE_STATUSES = ["available", "low", "out"] as const;

/**
 * A status this screen has a word for, or the raw value.
 *
 * The column is free text and the seed importer has written others; t() answers
 * with the key itself when it has never heard of one, and a pill reading
 * "petro.station.grade_status.suspended" is worse than the word the row holds.
 */
function knownGradeStatus(status: string): boolean {
  return (GRADE_STATUSES as readonly string[]).includes(status);
}

export default function FuelPage() {
  const { t } = useI18n();
  const [stations, setStations] = useState<FuelStation[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<string | null>(null);
  const [query, setQuery] = useState("");

  const load = useCallback(() => {
    api
      .listFuelStations()
      .then((result) => setStations(result.stations))
      .catch((err) => setError(err instanceof Error ? err.message : String(err)));
  }, []);

  useEffect(load, [load]);

  const shown = useMemo(() => {
    if (!stations) return null;
    const needle = query.trim().toLowerCase();
    if (!needle) return stations;
    return stations.filter((station) =>
      [station.name, station.aimag, station.district, station.address, station.brand_label]
        .join(" ")
        .toLowerCase()
        .includes(needle),
    );
  }, [stations, query]);

  return (
    <div>
      <div className="mb-6 flex flex-wrap items-start justify-between gap-4">
        <p className="max-w-2xl text-slate-500">{t("petro.station.subtitle")}</p>
        <PrimaryButton onClick={() => setAdding(true)} className="shrink-0">
          <Plus className="h-4 w-4" />
          {t("petro.station.add")}
        </PrimaryButton>
      </div>

      {error ? <ErrorNote>{error}</ErrorNote> : null}

      {adding ? (
        <StationForm
          onCancel={() => setAdding(false)}
          onDone={() => {
            setAdding(false);
            load();
          }}
        />
      ) : null}

      {stations && stations.length > 0 ? (
        <div className="mb-4 flex items-center justify-between gap-4">
          <input
            className={`${inputClass} max-w-xs`}
            placeholder={t("petro.station.search")}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
          <span className="text-sm text-slate-500">
            {t("petro.view.count")}: <b className="text-slate-900">{shown?.length ?? 0}</b>
          </span>
        </div>
      ) : null}

      {shown === null ? (
        <Loader2 className="h-5 w-5 animate-spin text-slate-400" />
      ) : shown.length === 0 ? (
        <section className="rounded-2xl border border-slate-200 bg-white p-10 text-center">
          <div className="mx-auto mb-4 grid h-14 w-14 place-items-center rounded-2xl bg-[var(--gerege-blue-soft)] text-[var(--gerege-blue)]">
            <Fuel className="h-7 w-7" />
          </div>
          <h2 className="text-lg font-semibold text-slate-900">{t("petro.view.empty_title")}</h2>
          <p className="mt-2 text-sm text-slate-500">{t("petro.view.empty_body")}</p>
        </section>
      ) : (
        <div className="grid gap-4">
          {shown.map((station) =>
            editing === station.id ? (
              <StationForm
                key={station.id}
                station={station}
                onCancel={() => setEditing(null)}
                onDone={() => {
                  setEditing(null);
                  load();
                }}
              />
            ) : (
              <StationCard
                key={station.id}
                station={station}
                onEdit={() => setEditing(station.id)}
                onChanged={load}
                onError={setError}
              />
            ),
          )}
        </div>
      )}
    </div>
  );
}

function StationCard({
  station,
  onEdit,
  onChanged,
  onError,
}: {
  station: FuelStation;
  onEdit: () => void;
  onChanged: () => void;
  onError: (message: string) => void;
}) {
  const { t } = useI18n();
  const [addingGrade, setAddingGrade] = useState(false);
  const grades = station.fuels ?? [];

  const remove = async () => {
    if (!window.confirm(t("petro.station.delete_confirm"))) return;
    try {
      await api.deleteFuelStation(station.id);
      onChanged();
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err));
    }
  };

  return (
    <section className="rounded-2xl border border-slate-200 bg-white p-5">
      <header className="mb-4 flex flex-wrap items-start justify-between gap-4">
        <div>
          <h2 className="flex flex-wrap items-center gap-2 font-semibold text-slate-900">
            {station.name}
            {station.brand_label ? (
              <span className="rounded-full bg-slate-100 px-2 py-0.5 text-xs font-normal text-slate-600">
                {station.brand_label}
              </span>
            ) : null}
            {station.is_voucher_enabled ? (
              <span
                className="inline-flex items-center gap-1 rounded-full bg-[var(--gerege-blue-soft)] px-2 py-0.5 text-xs font-normal text-[var(--gerege-blue)]"
                title={t("petro.station.voucher")}
              >
                <Ticket className="h-3 w-3" />
              </span>
            ) : null}
          </h2>
          <p className="mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-slate-500">
            <span className="inline-flex items-center gap-1">
              <MapPin className="h-3.5 w-3.5 text-slate-400" />
              {[station.aimag, station.district, station.address].filter(Boolean).join(" · ") || "—"}
            </span>
            {station.phone ? (
              <span className="inline-flex items-center gap-1">
                <Phone className="h-3.5 w-3.5 text-slate-400" />
                {station.phone}
              </span>
            ) : null}
            <span>
              {t("petro.station.pumps")}: {station.active_pumps}/{station.total_pumps}
            </span>
            <span>{station.opening_hours}</span>
          </p>
        </div>

        <div className="flex items-center gap-2">
          <GhostButton onClick={() => setAddingGrade(true)}>
            {t("petro.station.add_grade")}
          </GhostButton>
          <GhostButton onClick={onEdit}>
            <Pencil className="h-3.5 w-3.5" />
            {t("petro.station.edit")}
          </GhostButton>
          <button
            onClick={remove}
            title={t("petro.station.delete")}
            className="rounded-lg p-2 text-slate-400 transition-colors hover:bg-red-50 hover:text-red-600"
          >
            <Trash2 className="h-4 w-4" />
          </button>
        </div>
      </header>

      {addingGrade ? (
        <GradeRow
          stationID={station.id}
          onCancel={() => setAddingGrade(false)}
          onDone={() => {
            setAddingGrade(false);
            onChanged();
          }}
        />
      ) : null}

      {grades.length === 0 && !addingGrade ? (
        <p className="text-sm text-slate-400">{t("petro.station.no_grades")}</p>
      ) : (
        <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {grades.map((grade) => (
            <GradeCard
              key={grade.fuel_type}
              stationID={station.id}
              grade={grade}
              onChanged={onChanged}
              onError={onError}
            />
          ))}
        </ul>
      )}

      <p className="mt-3 text-xs text-slate-400">{t("petro.station.stock_note")}</p>
    </section>
  );
}

function gradeTone(status: string): string {
  if (status === "out") return "bg-red-100 text-red-700";
  if (status === "low") return "bg-amber-100 text-amber-700";
  return "bg-emerald-100 text-emerald-700";
}

function GradeCard({
  stationID,
  grade,
  onChanged,
  onError,
}: {
  stationID: string;
  grade: StationGrade;
  onChanged: () => void;
  onError: (message: string) => void;
}) {
  const { t } = useI18n();
  const [editing, setEditing] = useState(false);

  const remove = async () => {
    try {
      await api.deleteStationGrade(stationID, grade.fuel_type);
      onChanged();
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err));
    }
  };

  if (editing) {
    return (
      <li className="sm:col-span-2 lg:col-span-3">
        <GradeRow
          stationID={stationID}
          grade={grade}
          onCancel={() => setEditing(false)}
          onDone={() => {
            setEditing(false);
            onChanged();
          }}
        />
      </li>
    );
  }

  const percent =
    grade.tank_capacity_liters > 0
      ? (grade.current_stock_liters / grade.tank_capacity_liters) * 100
      : null;

  return (
    <li className="rounded-xl border border-slate-200 p-3">
      <div className="mb-2 flex items-baseline justify-between gap-2">
        <span className="text-sm font-medium text-slate-900">
          {grade.fuel_label || grade.fuel_type}
        </span>
        <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${gradeTone(grade.status)}`}>
          {knownGradeStatus(grade.status)
            ? t(`petro.station.grade_status.${grade.status}`)
            : grade.status}
        </span>
      </div>
      <p className="tabular-nums text-sm font-semibold text-slate-900">
        {Math.round(grade.price_mnt).toLocaleString("mn-MN")}₮
      </p>
      <p className="mt-1 text-xs text-slate-500">
        {percent === null ? (
          "—"
        ) : (
          <>
            <span className="tabular-nums">{litres(grade.current_stock_liters)}</span> /{" "}
            <span className="tabular-nums">{litres(grade.tank_capacity_liters)}</span> л ·{" "}
            {percent.toFixed(0)}%
          </>
        )}
      </p>
      <div className="mt-2 flex items-center gap-2">
        <button
          onClick={() => setEditing(true)}
          className="text-xs font-medium text-[var(--gerege-blue)] hover:underline"
        >
          {t("petro.station.edit")}
        </button>
        <button onClick={remove} className="text-xs text-slate-400 hover:text-red-600">
          {t("petro.station.delete")}
        </button>
      </div>
    </li>
  );
}

/** The price, the vessel and the availability of one grade. No litres — see the header. */
function GradeRow({
  stationID,
  grade,
  onCancel,
  onDone,
}: {
  stationID: string;
  grade?: StationGrade;
  onCancel: () => void;
  onDone: () => void;
}) {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [form, setForm] = useState({
    fuel_type: grade?.fuel_type ?? "ai92",
    price_mnt: grade ? String(Math.round(grade.price_mnt)) : "",
    tank_capacity_liters: grade ? String(Math.round(grade.tank_capacity_liters)) : "",
    status: grade?.status ?? "available",
  });

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.setStationGrade(stationID, {
        fuel_type: form.fuel_type,
        fuel_label: GRADES.find((g) => g.code === form.fuel_type)?.label ?? form.fuel_type,
        price_mnt: form.price_mnt === "" ? undefined : Number(form.price_mnt),
        tank_capacity_liters:
          form.tank_capacity_liters === "" ? undefined : Number(form.tank_capacity_liters),
        status: form.status,
      });
      onDone();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="mb-4 rounded-xl border border-slate-200 bg-slate-50 p-4">
      {error ? <p className="mb-3 text-sm text-red-600">{error}</p> : null}
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <Field label={t("petro.station.grade")}>
          <select
            className={inputClass}
            value={form.fuel_type}
            disabled={Boolean(grade)}
            onChange={(event) => setForm({ ...form, fuel_type: event.target.value })}
          >
            {GRADES.map((option) => (
              <option key={option.code} value={option.code}>
                {option.label}
              </option>
            ))}
          </select>
        </Field>
        <Field label={t("petro.station.price")}>
          <input
            type="number"
            min="0"
            className={inputClass}
            value={form.price_mnt}
            onChange={(event) => setForm({ ...form, price_mnt: event.target.value })}
          />
        </Field>
        <Field label={t("petro.station.capacity")}>
          <input
            type="number"
            min="0"
            className={inputClass}
            value={form.tank_capacity_liters}
            onChange={(event) => setForm({ ...form, tank_capacity_liters: event.target.value })}
          />
        </Field>
        <Field label={t("petro.station.status")}>
          <select
            className={inputClass}
            value={form.status}
            onChange={(event) => setForm({ ...form, status: event.target.value })}
          >
            {GRADE_STATUSES.map((status) => (
              <option key={status} value={status}>
                {t(`petro.station.grade_status.${status}`)}
              </option>
            ))}
          </select>
        </Field>
      </div>
      <div className="mt-4 flex items-center gap-2">
        <PrimaryButton type="submit" busy={busy}>
          {t("petro.station.save")}
        </PrimaryButton>
        <GhostButton type="button" onClick={onCancel}>
          {t("petro.station.cancel")}
        </GhostButton>
      </div>
    </form>
  );
}

/** Registering a forecourt, and correcting one. The same fields either way. */
function StationForm({
  station,
  onCancel,
  onDone,
}: {
  station?: FuelStation;
  onCancel: () => void;
  onDone: () => void;
}) {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [form, setForm] = useState({
    name: station?.name ?? "",
    brand_label: station?.brand_label ?? "",
    aimag: station?.aimag ?? "",
    district: station?.district ?? "",
    address: station?.address ?? "",
    phone: station?.phone ?? "",
    opening_hours: station?.opening_hours ?? "24/7",
    lat: station ? String(station.lat) : "",
    lon: station ? String(station.lon) : "",
    total_pumps: String(station?.total_pumps ?? 0),
    active_pumps: String(station?.active_pumps ?? 0),
    status: station?.status ?? "available",
    is_voucher_enabled: station?.is_voucher_enabled ?? true,
  });

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const shared = {
        name: form.name.trim(),
        brand_label: form.brand_label.trim(),
        aimag: form.aimag.trim(),
        district: form.district.trim(),
        address: form.address.trim(),
        phone: form.phone.trim(),
        opening_hours: form.opening_hours.trim() || "24/7",
        lat: Number(form.lat),
        lon: Number(form.lon),
        total_pumps: Number(form.total_pumps) || 0,
        active_pumps: Number(form.active_pumps) || 0,
        is_voucher_enabled: form.is_voucher_enabled,
      };
      if (station) {
        await api.updateFuelStation(station.id, { ...shared, status: form.status });
      } else {
        // The brand code is the slug the map and the import share; a company
        // registering its own forecourt has one brand, so it is derived rather
        // than asked for.
        await api.createFuelStation({
          ...shared,
          brand: form.brand_label.trim().toLowerCase().replace(/\s+/g, "-"),
        });
      }
      onDone();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="mb-6 rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
      {error ? <p className="mb-4 text-sm text-red-600">{error}</p> : null}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <Field label={t("petro.station.name")}>
          <input
            required
            className={inputClass}
            value={form.name}
            onChange={(event) => setForm({ ...form, name: event.target.value })}
          />
        </Field>
        <Field label={t("petro.station.brand")}>
          <input
            className={inputClass}
            value={form.brand_label}
            onChange={(event) => setForm({ ...form, brand_label: event.target.value })}
          />
        </Field>
        <Field label={t("petro.station.phone")}>
          <input
            className={inputClass}
            value={form.phone}
            onChange={(event) => setForm({ ...form, phone: event.target.value })}
          />
        </Field>
        <Field label={t("petro.station.aimag")}>
          <select
            className={inputClass}
            value={form.aimag}
            onChange={(event) => setForm({ ...form, aimag: event.target.value })}
          >
            <option value="">—</option>
            {AIMAGS.map((aimag) => (
              <option key={aimag}>{aimag}</option>
            ))}
          </select>
        </Field>
        <Field label={t("petro.station.district")}>
          <input
            className={inputClass}
            value={form.district}
            onChange={(event) => setForm({ ...form, district: event.target.value })}
          />
        </Field>
        <Field label={t("petro.station.address")}>
          <input
            className={inputClass}
            value={form.address}
            onChange={(event) => setForm({ ...form, address: event.target.value })}
          />
        </Field>
        <Field label={t("petro.station.lat")} hint={t("petro.station.coords_hint")}>
          <input
            required
            type="number"
            step="any"
            className={inputClass}
            value={form.lat}
            onChange={(event) => setForm({ ...form, lat: event.target.value })}
          />
        </Field>
        <Field label={t("petro.station.lon")}>
          <input
            required
            type="number"
            step="any"
            className={inputClass}
            value={form.lon}
            onChange={(event) => setForm({ ...form, lon: event.target.value })}
          />
        </Field>
        <Field label={t("petro.station.hours")}>
          <input
            className={inputClass}
            value={form.opening_hours}
            onChange={(event) => setForm({ ...form, opening_hours: event.target.value })}
          />
        </Field>
        <Field label={t("petro.station.pumps")}>
          <input
            type="number"
            min="0"
            className={inputClass}
            value={form.total_pumps}
            onChange={(event) => setForm({ ...form, total_pumps: event.target.value })}
          />
        </Field>
        <Field label={t("petro.station.active_pumps")}>
          <input
            type="number"
            min="0"
            className={inputClass}
            value={form.active_pumps}
            onChange={(event) => setForm({ ...form, active_pumps: event.target.value })}
          />
        </Field>
        {station ? (
          <Field label={t("petro.station.status")}>
            <select
              className={inputClass}
              value={form.status}
              onChange={(event) => setForm({ ...form, status: event.target.value })}
            >
              <option value="available">{t("petro.station.status.available")}</option>
              <option value="closed">{t("petro.station.status.closed")}</option>
            </select>
          </Field>
        ) : null}
      </div>

      <label className="mt-4 flex items-center gap-2 text-sm text-slate-700">
        <input
          type="checkbox"
          checked={form.is_voucher_enabled}
          onChange={(event) => setForm({ ...form, is_voucher_enabled: event.target.checked })}
        />
        {t("petro.station.voucher")}
      </label>

      <div className="mt-4 flex items-center gap-2">
        <PrimaryButton type="submit" busy={busy}>
          {t("petro.station.save")}
        </PrimaryButton>
        <GhostButton type="button" onClick={onCancel}>
          {t("petro.station.cancel")}
        </GhostButton>
      </div>
    </form>
  );
}
