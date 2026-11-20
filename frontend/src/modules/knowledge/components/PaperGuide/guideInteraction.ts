import type { PaperGuideInteractionParameter } from "@/api/generated/core-client";

const bounded = (value: number) => Number.isFinite(value) && Math.abs(value) <= 1e15;
const decimal = /^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$/;

const unary = new Map<string, [(value: number) => number, string]>([
  ["exp", [Math.exp, "exp"]],
  ["log", [Math.log, "ln"]],
  ["sqrt", [Math.sqrt, "√"]],
  ["neg", [(value) => -value, "−"]],
]);
const binary = new Map<string, [(left: number, right: number) => number, string]>([
  ["add", [(left, right) => left + right, "+"]],
  ["sub", [(left, right) => left - right, "−"]],
  ["mul", [(left, right) => left * right, "×"]],
  ["div", [(left, right) => left / right, "÷"]],
  ["pow", [Math.pow, "^"]],
]);

interface Expression {
  value: number | null;
  formula: string;
  substitution: string;
  compound?: boolean;
}

const operand = (text: string, compound = false) =>
  compound || /^[+-]/.test(text) ? `(${text})` : text;

export function evaluateGuideExpression(
  tokens: readonly string[],
  parameters: readonly PaperGuideInteractionParameter[],
  values?: ReadonlyMap<string, number>,
): Expression | null {
  if (tokens.length < 1 || tokens.length > 48) return null;
  const declared = new Map(parameters.map((parameter) => [parameter.id, parameter]));
  const stack: Expression[] = [];

  for (const token of tokens) {
    const one = unary.get(token);
    const two = binary.get(token);
    if (one) {
      const argument = stack.pop();
      if (!argument) return null;
      const [operation, symbol] = one;
      const result = argument.value === null ? NaN : operation(argument.value);
      stack.push({
        value: bounded(result) ? result : null,
        formula: `${symbol}(${argument.formula})`,
        substitution: `${symbol}(${argument.substitution})`,
        compound: token === "neg",
      });
    } else if (two) {
      const right = stack.pop();
      const left = stack.pop();
      if (!left || !right) return null;
      const [operation, symbol] = two;
      const result = left.value === null || right.value === null ? NaN : operation(left.value, right.value);
      stack.push({
        value: bounded(result) ? result : null,
        formula: `${operand(left.formula, left.compound)} ${symbol} ${operand(right.formula, right.compound)}`,
        substitution: `${operand(left.substitution, left.compound)} ${symbol} ${operand(right.substitution, right.compound)}`,
        compound: true,
      });
    } else {
      const parameter = declared.get(token);
      if (!parameter && !decimal.test(token)) return null;
      const value = parameter ? (values?.get(token) ?? parameter.value) : Number(token);
      stack.push({
        value: bounded(value) && (!parameter || (value >= parameter.min && value <= parameter.max)) ? value : null,
        formula: token,
        substitution: parameter ? String(value) : token,
      });
    }
  }
  return stack.length === 1 ? stack[0] : null;
}
