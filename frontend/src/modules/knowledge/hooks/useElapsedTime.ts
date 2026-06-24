import moment from "moment";
import { useEffect, useRef, useState } from "react";

interface IProps {
  startTime?: number | string;
  endTime?: number | string;
}

interface IResult {
  days: number;
  hours: number;
  minutes: number;
  seconds: number;
}

const useElapsedTime = (props: IProps) => {
  const [result, setResult] = useState<IResult>({
    days: 0,
    hours: 0,
    minutes: 0,
    seconds: 0,
  });
  const timeoutRef = useRef<any>(null);
  const { startTime, endTime } = props;
  useEffect(() => {
    updateTime();
    return () => {
      clearTimeout(timeoutRef.current);
    };
  }, [startTime, endTime]);

  const updateTime = () => {
    const start = parseTime(startTime);
    if (!start) {
      setResult({
        days: 0,
        hours: 0,
        minutes: 0,
        seconds: 0,
      });
      return;
    }
    const end = parseTime(endTime) || moment();
    const totalSeconds = Math.max(
      0,
      Math.floor((end.valueOf() - start.valueOf()) / 1000),
    );
    const hours = Math.floor(totalSeconds / 3600);
    const minutes = Math.floor((totalSeconds % 3600) / 60);
    const seconds = totalSeconds % 60;
    setResult({ days: 0, hours, minutes, seconds });

    if (!endTime) {
      timeoutRef.current = setTimeout(() => {
        updateTime();
      }, 1000);
    }
  };

  return result;
};

function parseTime(value?: number | string) {
  if (value === undefined || value === null || value === "" || value === 0 || value === "0") {
    return null;
  }
  const text = String(value).trim();
  const numeric = Number(text);
  if (Number.isFinite(numeric) && text !== "") {
    if (numeric >= 1_000_000_000 && numeric < 1_000_000_000_000) {
      return moment(numeric * 1000);
    }
    if (numeric >= 1_000_000_000_000) {
      return moment(numeric);
    }
  }
  const parsed = moment(text);
  return parsed.isValid() ? parsed : null;
}

export default useElapsedTime;
