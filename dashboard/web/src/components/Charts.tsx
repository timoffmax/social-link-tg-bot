import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  LabelList,
  Rectangle,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
  type TooltipProps,
} from 'recharts';
import { formatCompact, formatNumber, tickInterval } from '../format';
import { Legend } from './Bits';

export const SERIES_1 = 'var(--series-1)';
export const SERIES_2 = 'var(--series-2)';

const AXIS_TICK = { fill: 'var(--text-muted)', fontSize: 12 };
const MAX_BAR = 24;
const GAP_THRESHOLD = 120;
const MAX_X_TICKS = 7;
const AXIS_WIDTH = 40;
const CURSOR = { fill: 'var(--hover-wash)' };
const LINE_CURSOR = { stroke: 'var(--axis)', strokeWidth: 1 };
const SEGMENT_GAP = 2;
const ACTIVE_OPACITY = 0.8;

interface BarShapeProps {
  x: number;
  y: number;
  width: number;
  height: number;
  fill: string;
  radius?: number | [number, number, number, number];
}

/**
 * A bar mark with a surface gap cut out of its baseline side, so stacked
 * segments read apart without drawing a border around them.
 */
function gapShape(orientation: 'vertical' | 'horizontal', trim: boolean, opacity: number) {
  return (props: unknown) => {
    const bar = props as BarShapeProps;
    const isVertical = (orientation === 'vertical');
    const size = isVertical ? bar.height : bar.width;
    const gap = (trim && (size > SEGMENT_GAP)) ? SEGMENT_GAP : 0;
    const x = isVertical ? bar.x : (bar.x + gap);
    const width = isVertical ? bar.width : (bar.width - gap);
    const height = isVertical ? (bar.height - gap) : bar.height;

    return <Rectangle x={x} y={bar.y} width={width} height={height} fill={bar.fill} fillOpacity={opacity} radius={bar.radius} />;
  };
}

export interface Series {
  key: string;
  label: string;
  color: string;
}

type Row = object;

interface TooltipContentProps extends TooltipProps<number, string> {
  series: Series[];
  formatLabel: (label: string) => string;
  showTotal: boolean;
  extra?: (row: Row) => { label: string; value: string }[];
}

function TooltipContent({ active, payload, label, series, formatLabel, showTotal, extra }: TooltipContentProps) {
  const row = payload?.[0]?.payload as Row | undefined;
  const isVisible = (active === true) && (row !== undefined);
  const values = series.map((item) => ({ ...item, value: Number((row as Record<string, unknown> | undefined)?.[item.key] ?? 0) }));
  const total = values.reduce((sum, item) => sum + item.value, 0);
  const extraRows = ((extra !== undefined) && (row !== undefined)) ? extra(row) : [];

  return isVisible ? (
    <div className="tooltip">
      <div className="tooltip__label">{formatLabel(String(label))}</div>
      {values.map((item) => (
        <div key={item.key} className="tooltip__row">
          <span className="tooltip__key" style={{ background: item.color }} aria-hidden="true" />
          <strong>{formatNumber(item.value)}</strong>
          <span>{item.label}</span>
        </div>
      ))}
      {(showTotal && (series.length > 1)) && (
        <div className="tooltip__row tooltip__row--total">
          <span className="tooltip__key" aria-hidden="true" />
          <strong>{formatNumber(total)}</strong>
          <span>Total</span>
        </div>
      )}
      {extraRows.map((item) => (
        <div key={item.label} className="tooltip__row tooltip__row--extra">
          <span className="tooltip__key" aria-hidden="true" />
          <strong>{item.value}</strong>
          <span>{item.label}</span>
        </div>
      ))}
    </div>
  ) : null;
}

interface ColumnChartProps<T extends Row> {
  data: T[];
  xKey: keyof T & string;
  series: Series[];
  stacked: boolean;
  formatTick: (value: string) => string;
  formatLabel: (value: string) => string;
  height?: number;
  extra?: (row: T) => { label: string; value: string }[];
}

/** Vertical bars over time: stacked (part of a whole) or grouped. */
export function ColumnChart<T extends Row>(props: ColumnChartProps<T>) {
  const { data, xKey, series, stacked, formatTick, formatLabel, extra } = props;
  const height = props.height ?? 240;
  const dense = (data.length > GAP_THRESHOLD);
  const lastIndex = series.length - 1;

  return (
    <figure className="chart">
      {(series.length > 1) && <Legend items={series} />}
      <ResponsiveContainer width="100%" height={height}>
        <BarChart data={data} margin={{ top: 8, right: 4, bottom: 0, left: 0 }} barCategoryGap={dense ? 0 : '18%'} barGap={0}>
          <CartesianGrid vertical={false} stroke="var(--grid)" />
          <XAxis
            dataKey={xKey}
            tickFormatter={formatTick}
            interval={tickInterval(data.length, MAX_X_TICKS)}
            tick={AXIS_TICK}
            tickLine={false}
            axisLine={{ stroke: 'var(--axis)' }}
            minTickGap={8}
          />
          <YAxis
            tickFormatter={formatCompact}
            tick={AXIS_TICK}
            tickLine={false}
            axisLine={false}
            allowDecimals={false}
            width={AXIS_WIDTH}
          />
          <Tooltip
            cursor={CURSOR}
            isAnimationActive={false}
            content={(tooltip: TooltipProps<number, string>) => (
              <TooltipContent
                {...tooltip}
                series={series}
                formatLabel={formatLabel}
                showTotal={stacked}
                extra={extra as ((row: Row) => { label: string; value: string }[]) | undefined}
              />
            )}
          />
          {series.map((item, index) => {
            const isTop = (stacked === false) || (index === lastIndex);
            const trim = stacked && (index > 0) && (dense === false);

            return (
              <Bar
                key={item.key}
                dataKey={item.key}
                name={item.label}
                stackId={stacked ? 'stack' : undefined}
                fill={item.color}
                radius={(isTop && (dense === false)) ? [4, 4, 0, 0] : 0}
                maxBarSize={MAX_BAR}
                isAnimationActive={false}
                shape={gapShape('vertical', trim, 1)}
                activeBar={gapShape('vertical', trim, ACTIVE_OPACITY)}
              />
            );
          })}
        </BarChart>
      </ResponsiveContainer>
    </figure>
  );
}

interface BarListProps<T extends Row> {
  data: T[];
  labelKey: keyof T & string;
  valueKey: keyof T & string;
  label: string;
  formatCategory: (value: string) => string;
}

const BAR_ROW = 30;

/** Horizontal bars for a ranking: one series, one color, value at the tip. */
export function BarList<T extends Row>({ data, labelKey, valueKey, label, formatCategory }: BarListProps<T>) {
  const height = (data.length * BAR_ROW) + 8;
  const series: Series[] = [{ key: valueKey, label, color: SERIES_1 }];

  return (
    <figure className="chart">
      <ResponsiveContainer width="100%" height={height}>
        <BarChart data={data} layout="vertical" margin={{ top: 0, right: 56, bottom: 0, left: 0 }} barCategoryGap={6}>
          <XAxis type="number" hide />
          <YAxis
            type="category"
            dataKey={labelKey}
            tickFormatter={formatCategory}
            tick={{ ...AXIS_TICK, fill: 'var(--text-secondary)' }}
            tickLine={false}
            axisLine={{ stroke: 'var(--axis)' }}
            width={88}
          />
          <Tooltip
            cursor={CURSOR}
            isAnimationActive={false}
            content={(tooltip: TooltipProps<number, string>) => (
              <TooltipContent {...tooltip} series={series} formatLabel={formatCategory} showTotal={false} />
            )}
          />
          <Bar dataKey={valueKey} name={label} fill={SERIES_1} radius={[0, 4, 4, 0]} maxBarSize={18} isAnimationActive={false} activeBar={{ fillOpacity: ACTIVE_OPACITY }}>
            <LabelList dataKey={valueKey} position="right" formatter={formatCompact} className="bar-label" />
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </figure>
  );
}

interface StackedBarListProps<T extends Row> {
  data: T[];
  labelKey: keyof T & string;
  series: Series[];
  formatCategory: (value: string) => string;
}

/** Horizontal stacked bars (part of a whole per category). */
export function StackedBarList<T extends Row>({ data, labelKey, series, formatCategory }: StackedBarListProps<T>) {
  const height = (data.length * BAR_ROW) + 8;
  const lastIndex = series.length - 1;

  return (
    <figure className="chart">
      <Legend items={series} />
      <ResponsiveContainer width="100%" height={height}>
        <BarChart data={data} layout="vertical" margin={{ top: 0, right: 8, bottom: 0, left: 0 }} barCategoryGap={6}>
          <XAxis type="number" hide />
          <YAxis
            type="category"
            dataKey={labelKey}
            tickFormatter={formatCategory}
            tick={{ ...AXIS_TICK, fill: 'var(--text-secondary)' }}
            tickLine={false}
            axisLine={{ stroke: 'var(--axis)' }}
            width={88}
          />
          <Tooltip
            cursor={CURSOR}
            isAnimationActive={false}
            content={(tooltip: TooltipProps<number, string>) => (
              <TooltipContent {...tooltip} series={series} formatLabel={formatCategory} showTotal />
            )}
          />
          {series.map((item, index) => (
            <Bar
              key={item.key}
              dataKey={item.key}
              name={item.label}
              stackId="stack"
              fill={item.color}
              radius={(index === lastIndex) ? [0, 4, 4, 0] : 0}
              maxBarSize={18}
              isAnimationActive={false}
              shape={gapShape('horizontal', (index > 0), 1)}
              activeBar={gapShape('horizontal', (index > 0), ACTIVE_OPACITY)}
            />
          ))}
        </BarChart>
      </ResponsiveContainer>
    </figure>
  );
}

interface SparkAreaProps {
  points: { day: string; value: number }[];
  label: string;
  formatLabel: (value: string) => string;
}

/** One small multiple: a 2px line with a 10% wash, own y-scale, crosshair tooltip. */
export function SparkArea({ points, label, formatLabel }: SparkAreaProps) {
  const series: Series[] = [{ key: 'value', label, color: SERIES_1 }];

  return (
    <ResponsiveContainer width="100%" height={72}>
      <AreaChart data={points} margin={{ top: 4, right: 2, bottom: 0, left: 2 }}>
        <XAxis dataKey="day" hide />
        <YAxis hide domain={[0, 'dataMax']} />
        <Tooltip
          cursor={LINE_CURSOR}
          isAnimationActive={false}
          content={(tooltip: TooltipProps<number, string>) => (
            <TooltipContent {...tooltip} series={series} formatLabel={formatLabel} showTotal={false} />
          )}
        />
        <Area
          type="linear"
          dataKey="value"
          name={label}
          stroke={SERIES_1}
          strokeWidth={2}
          strokeLinejoin="round"
          strokeLinecap="round"
          fill={SERIES_1}
          fillOpacity={0.1}
          isAnimationActive={false}
          activeDot={{ r: 4, stroke: 'var(--surface)', strokeWidth: 2, fill: SERIES_1 }}
        />
      </AreaChart>
    </ResponsiveContainer>
  );
}
