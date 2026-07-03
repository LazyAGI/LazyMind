import { Button, Checkbox, Form, Space, TimePicker, Typography } from "antd";
import type { FormInstance } from "antd";
import { CalendarOutlined, ClockCircleOutlined } from "@ant-design/icons";
import dayjs from "dayjs";
import type { TFunction } from "i18next";
import type { SourceFormValues } from "../../constants/types";
import {
  SCHEDULE_WEEKDAY_DISPLAY_ORDER,
  SCHEDULE_WEEKDAYS,
  SCHEDULE_WEEKENDS,
  SCHEDULE_WORKDAYS,
  isSameWeekdaySet,
  normalizeSelectedWeekdays,
  toggleShortcutWeekdays,
} from "./scheduleUtils";

const { Text } = Typography;

export interface WizardSchedulePanelProps {
  t: TFunction;
  form: FormInstance<SourceFormValues>;
}

export default function WizardSchedulePanel({ t, form }: WizardSchedulePanelProps) {
  const selectedScheduleWeekdays = normalizeSelectedWeekdays(
    Form.useWatch("scheduleWeekdays", form),
  );
  const isWorkdaysSelected = isSameWeekdaySet(selectedScheduleWeekdays, SCHEDULE_WORKDAYS);
  const isWeekendsSelected = isSameWeekdaySet(selectedScheduleWeekdays, SCHEDULE_WEEKENDS);
  const isEverydaySelected = isSameWeekdaySet(selectedScheduleWeekdays, SCHEDULE_WEEKDAYS);

  return (
    <div className="data-source-schedule-panel">
      <div className="data-source-schedule-panel-head">
        <ClockCircleOutlined />
        <Text strong>{t("admin.dataSourceScheduleTitle")}</Text>
      </div>
      <div className="data-source-schedule-inline-builder">
        <div className="data-source-schedule-inline-toolbar">
          <Space wrap className="data-source-schedule-shortcuts">
            <Button
              size="small"
              className={isWorkdaysSelected ? "is-active" : ""}
              onClick={() =>
                form.setFieldValue(
                  "scheduleWeekdays",
                  toggleShortcutWeekdays(selectedScheduleWeekdays, SCHEDULE_WORKDAYS),
                )
              }
            >
              {t("admin.dataSourceScheduleShortcutWorkdays")}
            </Button>
            <Button
              size="small"
              className={isWeekendsSelected ? "is-active" : ""}
              onClick={() =>
                form.setFieldValue(
                  "scheduleWeekdays",
                  toggleShortcutWeekdays(selectedScheduleWeekdays, SCHEDULE_WEEKENDS),
                )
              }
            >
              {t("admin.dataSourceScheduleShortcutWeekends")}
            </Button>
            <Button
              size="small"
              className={isEverydaySelected ? "is-active" : ""}
              onClick={() =>
                form.setFieldValue(
                  "scheduleWeekdays",
                  toggleShortcutWeekdays(selectedScheduleWeekdays, SCHEDULE_WEEKDAYS),
                )
              }
            >
              {t("admin.dataSourceScheduleShortcutEveryday")}
            </Button>
          </Space>
        </div>
        <div className="data-source-schedule-inline-sentence">
          <div className="data-source-schedule-inline-icon">
            <CalendarOutlined />
            <ClockCircleOutlined />
          </div>
          <div className="data-source-schedule-inline-content">
            <Text className="data-source-schedule-inline-prefix">
              {t("admin.dataSourceScheduleSelectDaysPrefix")}
            </Text>
            <div className="data-source-schedule-inline-controls">
              <Text className="data-source-schedule-inline-cycle">
                {t("admin.dataSourceScheduleWeekly")}
              </Text>
              <Form.Item
                name="scheduleWeekdays"
                className="data-source-schedule-inline-weekdays-item"
                rules={[
                  {
                    required: true,
                    message: t("admin.dataSourceScheduleWeekdaysRequired"),
                  },
                ]}
              >
                <Checkbox.Group className="data-source-schedule-weekdays">
                  {SCHEDULE_WEEKDAY_DISPLAY_ORDER.map((day) => (
                    <Checkbox key={day} value={day}>
                      <span className="data-source-schedule-weekday-pill">
                        {t(`admin.dataSourceScheduleWeekdayShort${day}`)}
                      </span>
                    </Checkbox>
                  ))}
                </Checkbox.Group>
              </Form.Item>
              <Text className="data-source-schedule-inline-connector">
                {t("admin.dataSourceScheduleTimeConnector")}
              </Text>
              <Form.Item
                name="scheduleTime"
                className="data-source-schedule-inline-time-item"
                getValueProps={(value?: string) => ({
                  value: value ? dayjs(value, "HH:mm:ss") : null,
                })}
                normalize={(value: ReturnType<typeof dayjs> | null) =>
                  value ? value.format("HH:mm:ss") : undefined
                }
                rules={[
                  {
                    required: true,
                    message: t("admin.dataSourceScheduleTimeRequired"),
                  },
                  {
                    pattern: /^([01]\d|2[0-3]):[0-5]\d:[0-5]\d$/,
                    message: t("admin.dataSourceScheduleTimeInvalid"),
                  },
                ]}
              >
                <TimePicker
                  className="data-source-schedule-time-picker"
                  format="HH:mm:ss"
                  needConfirm={false}
                  showNow={false}
                />
              </Form.Item>
              <Text className="data-source-schedule-inline-suffix">
                {t("admin.dataSourceScheduleTimeSuffix")}
              </Text>
            </div>
          </div>
          <div className="data-source-schedule-visual" aria-hidden="true">
            <CalendarOutlined />
            <ClockCircleOutlined />
          </div>
        </div>
      </div>
    </div>
  );
}
