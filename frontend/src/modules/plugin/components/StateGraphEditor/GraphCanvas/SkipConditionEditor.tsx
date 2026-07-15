import { Button, Select } from 'antd';
import { PlusOutlined } from '@ant-design/icons';
import type { MaterialExpression, SlotDef } from '../core/model';

interface Props {
  value?: MaterialExpression;
  slots: Record<string, SlotDef>;
  readonly?: boolean;
  onChange: (value: MaterialExpression) => void;
}

type MatchMode = 'all' | 'any';

function flatten(value?: MaterialExpression): { mode: MatchMode; materials: string[] } {
  if (value?.any) return { mode: 'any', materials: value.any.map((item) => item.material ?? '') };
  if (value?.all) return { mode: 'all', materials: value.all.map((item) => item.material ?? '') };
  return { mode: 'all', materials: [value?.material ?? ''] };
}

function build(mode: MatchMode, materials: string[]): MaterialExpression {
  return { [mode]: materials.map((material) => ({ material })) };
}

export default function SkipConditionEditor({ value, slots, readonly = false, onChange }: Props) {
  const { mode, materials: parsedMaterials } = flatten(value);
  const materials = parsedMaterials.length > 0 ? parsedMaterials : [''];
  const options = Object.values(slots).map((slot) => ({
    value: slot.id,
    label: slot.label ? `${slot.id} (${slot.label})` : slot.id,
  }));

  const updateMaterial = (index: number, material: string) => {
    const next = [...materials];
    next[index] = material;
    onChange(build(mode, next));
  };

  return (
    <div className="npp-skip-condition">
      <div className="npp-skip-sentence">当满足在本步骤执行时，</div>
      <div className="npp-skip-materials">
        {materials.map((material, index) => (
          <div className="npp-skip-material-row" key={index}>
            <Select
              value={material || undefined}
              options={options.filter((option) => option.value === material || !materials.includes(option.value))}
              placeholder="选择素材"
              disabled={readonly}
              size="small"
              showSearch
              allowClear
              optionFilterProp="label"
              onChange={(next) => {
                if (!next && materials.length > 1) {
                  onChange(build(mode, materials.filter((_, itemIndex) => itemIndex !== index)));
                  return;
                }
                updateMaterial(index, next ?? '');
              }}
            />
            {index === materials.length - 1 && (
              <Button
                type="text"
                size="small"
                icon={<PlusOutlined />}
                disabled={readonly}
                aria-label="添加跳过条件素材"
                onClick={() => onChange(build(mode, [...materials, '']))}
              />
            )}
          </div>
        ))}
      </div>
      <div className="npp-skip-sentence npp-skip-result">
        已经
        <Select
          value={mode}
          size="small"
          disabled={readonly}
          options={[
            { value: 'all', label: '同时' },
            { value: 'any', label: '任意' },
          ]}
          onChange={(next: MatchMode) => onChange(build(next, materials))}
        />
        被提供时，跳过此步骤
      </div>
    </div>
  );
}
