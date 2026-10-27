export function sortCustomModelsFirst<T extends { builtIn: boolean }>(models: readonly T[]): T[] {
  return [...models].sort((left, right) => Number(left.builtIn) - Number(right.builtIn));
}
