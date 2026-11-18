import { useId, useState } from "react";
import type { PaperGuideInteraction as Interaction } from "@/api/generated/core-client";
import { evaluateGuideExpression } from "./guideInteraction";
import PaperGuideText from "./PaperGuideText";
import "./PaperGuideInteraction.scss";

interface Props {
  interaction: Interaction;
  language: string;
}

const quantity = (value: string | number, unit: string) =>
  unit ? `${value} ${unit}` : String(value);

function Stepper({ steps, zh }: { steps: Interaction["steps"]; zh: boolean }) {
  const [index, setIndex] = useState(0);
  const selected = steps[index];
  if (!selected) return null;
  return (
    <>
      <ol className="guide-interaction__steps">
        {steps.map(({ label }, i) => (
          <li key={i}>
            <button type="button" aria-current={index === i ? "step" : undefined} onClick={() => setIndex(i)}>
              <span className="guide-interaction__step-number">{i + 1}</span> {label}
            </button>
          </li>
        ))}
      </ol>
      <div className="guide-interaction__panel" aria-live="polite">
        <strong>{selected.label}</strong>
        <p><PaperGuideText mapSource={false} text={selected.text} /></p>
      </div>
      <div className="guide-interaction__actions">
        <button type="button" disabled={index === 0} onClick={() => setIndex(index - 1)}>{zh ? "上一步" : "Previous"}</button>
        <button type="button" disabled={index === steps.length - 1} onClick={() => setIndex(index + 1)}>{zh ? "下一步" : "Next"}</button>
        <button type="button" onClick={() => setIndex(0)}>{zh ? "重置" : "Reset"}</button>
      </div>
    </>
  );
}

function Comparison({ steps, zh }: { steps: Interaction["steps"]; zh: boolean }) {
  const [index, setIndex] = useState(1);
  const first = steps[0];
  const selected = steps[index];
  if (!first || !selected) return null;
  return (
    <>
      {steps.length > 2 && (
        <div className="guide-interaction__actions" role="group" aria-label={zh ? "选择对照项" : "Choose a comparison"}>
          {steps.slice(1).map(({ label }, i) => (
            <button type="button" key={i} aria-pressed={index === i + 1} onClick={() => setIndex(i + 1)}>
              {label}
            </button>
          ))}
        </div>
      )}
      <div className="guide-interaction__comparison" aria-live="polite">
        {[first, selected].map(({ label, text }, i) => (
          <div className="guide-interaction__panel" key={i}>
            <strong>{label}</strong>
            <p><PaperGuideText mapSource={false} text={text} /></p>
          </div>
        ))}
      </div>
    </>
  );
}

function Results({ interaction, zh }: { interaction: Interaction; zh: boolean }) {
  const { rows, metric, unit, higher_is_better } = interaction;
  const [filter, setFilter] = useState(0);
  const groups = [...new Set(rows.map(({ group }) => group))];
  const shown = rows.filter(({ group }) => !filter || group === groups[filter - 1]);

  // Normalize first to avoid overflow when the signed data span a wide interval.
  // Keep this scale over all reported rows, including those hidden by the filter.
  const magnitude = Math.max(1, ...rows.map(({ value }) => Math.abs(value)));
  const min = Math.min(0, ...rows.map(({ value }) => value / magnitude));
  const max = Math.max(0, ...rows.map(({ value }) => value / magnitude));
  const span = max - min;
  const percent = (normalized: number) => span ? (normalized - min) * 100 / span : 0;
  const zero = percent(0);
  const caption = unit ? `${metric} (${unit})` : metric;
  return (
    <>
      {groups.length > 1 && (
        <label className="guide-interaction__filter">
          {zh ? "按组筛选" : "Filter by group"}
          <select value={filter} onChange={(event) => setFilter(Number(event.target.value))}>
            <option value={0}>{zh ? "所有组" : "All groups"}</option>
            {groups.map((group, index) => (
              <option key={group} value={index + 1}>{group || (zh ? "未分组" : "Ungrouped")}</option>
            ))}
          </select>
        </label>
      )}
      <p className="guide-interaction__direction">
        {higher_is_better ? (zh ? "越高越好" : "Higher is better") : (zh ? "越低越好" : "Lower is better")}
      </p>
      <table className="guide-interaction__results">
        <caption>{caption}</caption>
        <thead>
          <tr>
            <th scope="col">{zh ? "观测" : "Observation"}</th>
            <th scope="col">{metric}</th>
          </tr>
        </thead>
        <tbody>
          {shown.map(({ label, value, note, group }, i) => {
            const end = percent(value / magnitude);
            return (
              <tr key={i}>
                <th scope="row">
                  <div>{label}</div>
                  {group && <small>{group}</small>}
                  {note && <small><PaperGuideText mapSource={false} text={note} /></small>}
                </th>
                <td>
                  <span>{value}</span>
                  <div aria-hidden className="guide-interaction__track">
                    <span className="guide-interaction__bar" style={{ left: `${Math.min(zero, end)}%`, width: `${Math.abs(end - zero)}%` }} />
                    <span className="guide-interaction__zero" style={{ left: `${zero}%` }} />
                  </div>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </>
  );
}

function alignedToStep(value: number, parameter: Interaction["parameters"][number]) {
  const index = (value - parameter.min) / parameter.step;
  return Number.isFinite(index) && Math.abs(index - Math.round(index)) < 1e-8;
}

function Calculator({ interaction, zh }: { interaction: Interaction; zh: boolean }) {
  const { parameters, outputs } = interaction;
  const id = useId();
  const [inputs, setInputs] = useState(new Map<string, string>());
  const values = new Map(parameters.map((parameter) => {
    const input = inputs.get(parameter.id);
    const value = input === undefined ? parameter.value : input === "" ? NaN : Number(input);
    return [parameter.id, value === parameter.value || alignedToStep(value, parameter) ? value : NaN];
  }));
  let unavailable = false;
  return (
    <>
      <div className="guide-interaction__parameters">
        {parameters.map((parameter) => {
          const inputId = `${id}-${parameter.id}`;
          const value = inputs.get(parameter.id) ?? String(parameter.value);
          // HTML ranges round values not aligned to the step grid. Preserve an
          // explicit paper default even when the supplied step does not land on it.
          const aligned = alignedToStep(parameter.value, parameter);
          const numericValue = values.get(parameter.id);
          const valid = numericValue !== undefined && numericValue >= parameter.min && numericValue <= parameter.max;
          return (
            <div className="guide-interaction__panel" key={parameter.id}>
              <label htmlFor={inputId}>{parameter.label}</label>
              {aligned && <span className="guide-interaction__parameter-value">{quantity(value, parameter.unit)}</span>}
              <input
                id={inputId}
                type={aligned ? "range" : "number"}
                min={parameter.min}
                max={parameter.max}
                step={aligned ? parameter.step : "any"}
                value={value}
                aria-valuetext={aligned ? quantity(value, parameter.unit) : undefined}
                aria-describedby={aligned ? undefined : `${inputId}-increments`}
                aria-invalid={valid ? undefined : true}
                onChange={(event) => {
                  const next = event.currentTarget.value;
                  setInputs((previous) => new Map(previous).set(parameter.id, next));
                }}
              />
              {!aligned && (
                <small className="guide-interaction__direction" id={`${inputId}-increments`}>
                  {zh
                    ? `可输入默认值 ${quantity(parameter.value, parameter.unit)}，或从 ${quantity(parameter.min, parameter.unit)} 起按 ${quantity(parameter.step, parameter.unit)} 递增。`
                    : `Enter the default (${quantity(parameter.value, parameter.unit)}) or use increments of ${quantity(parameter.step, parameter.unit)} from ${quantity(parameter.min, parameter.unit)}.`}
                </small>
              )}
              <div className="guide-interaction__range" aria-hidden={aligned}>
                <span>{quantity(parameter.min, parameter.unit)}</span>
                <span>{quantity(parameter.max, parameter.unit)}</span>
              </div>
            </div>
          );
        })}
      </div>
      <div className="guide-interaction__actions">
        <button type="button" onClick={() => setInputs(new Map())}>{zh ? "重置" : "Reset"}</button>
      </div>
      <dl className="guide-interaction__outputs">
        {outputs.map(({ label, tokens, unit }, index) => {
          const initial = evaluateGuideExpression(tokens, parameters);
          const current = evaluateGuideExpression(tokens, parameters, values);
          unavailable ||= current?.value == null;
          return (
            <div className="guide-interaction__panel" key={index}>
              <dt>{label}</dt>
              <dd>
                {initial && (
                  <div className="guide-interaction__formula">
                    <code>{initial.formula}</code>
                    <small>
                      {zh ? "默认代入：" : "At defaults: "}
                      <span>{initial.substitution} = {initial.value === null ? "N/A" : quantity(initial.value, unit)}</span>
                    </small>
                  </div>
                )}
                <output aria-label={label}>
                  {current?.value == null ? "N/A" : quantity(current.value, unit)}
                </output>
              </dd>
            </div>
          );
        })}
      </dl>
      {unavailable && (
        <p role="status">
          {zh ? "当前输入超出公式支持的取值范围或定义域。" : "The current inputs are outside the supported range or domain."}
        </p>
      )}
    </>
  );
}

export default function PaperGuideInteraction({ interaction, language }: Props) {
  const zh = language === "zh";
  const id = useId();
  const provenance = {
    reported: zh ? "论文报告值" : "Reported by the paper",
    derived: zh ? "根据论文推导" : "Derived from the paper",
    illustrative: zh ? "教学示例" : "Illustrative example",
  }[interaction.provenance];
  return (
    <div className="guide-interaction" role="group" aria-labelledby={id}>
      <div className="guide-interaction__header">
        <strong id={id}>{interaction.title}</strong>
        <span className="guide-interaction__provenance">{provenance}</span>
      </div>
      <p className="guide-interaction__explanation"><PaperGuideText mapSource={false} text={interaction.explanation} /></p>
      {interaction.kind === "stepper" && <Stepper steps={interaction.steps} zh={zh} />}
      {interaction.kind === "comparison" && <Comparison steps={interaction.steps} zh={zh} />}
      {interaction.kind === "results" && interaction.provenance === "reported" && <Results interaction={interaction} zh={zh} />}
      {interaction.kind === "calculator" && <Calculator interaction={interaction} zh={zh} />}
    </div>
  );
}
