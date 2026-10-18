import { Fragment, type ReactNode } from 'react';
import { useIsPhone } from '../hooks';

export interface Column<T> {
  header: string;
  cell: (row: T) => ReactNode;
  numeric?: boolean;
  /** On a phone the column takes the room left over and ellipsizes its title. */
  fill?: boolean;
  /** On a phone the column moves into a line under its row instead of its own column. */
  detail?: boolean;
}

interface DataTableProps<T> {
  columns: Column<T>[];
  rows: T[];
  rowKey: (row: T) => string | number;
  caption?: string;
}

function cellClass<T>(column: Column<T>): string | undefined {
  const names = [
    (column.numeric === true) ? 'num' : '',
    (column.fill === true) ? 'cell-fill' : '',
  ].filter((name) => name !== '');
  const result = (names.length > 0) ? names.join(' ') : undefined;

  return result;
}

interface DetailRowProps<T> {
  row: T;
  columns: Column<T>[];
  span: number;
}

/** The phone-only line under a row with the values of its detail columns. */
function DetailRow<T>({ row, columns, span }: DetailRowProps<T>) {
  return (
    <tr className="table__detail">
      <td colSpan={span}>
        <dl className="cell-details">
          {columns.map((column) => (
            <div key={column.header}>
              <dt>{column.header}</dt>
              <dd>{column.cell(row)}</dd>
            </div>
          ))}
        </dl>
      </td>
    </tr>
  );
}

export function DataTable<T>({ columns, rows, rowKey, caption }: DataTableProps<T>) {
  const isPhone = useIsPhone();
  const detailColumns = columns.filter((column) => (column.detail === true));
  const hasDetail = isPhone && (detailColumns.length > 0);
  // A phone shows the detail columns once, in the line under their row.
  const shownColumns = hasDetail ? columns.filter((column) => (column.detail !== true)) : columns;

  return (
    <div className="table-scroll">
      <table className="table">
        {(caption !== undefined) && <caption className="visually-hidden">{caption}</caption>}
        <thead>
          <tr>
            {shownColumns.map((column) => (
              <th key={column.header} scope="col" className={cellClass(column)}>
                {column.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <Fragment key={rowKey(row)}>
              <tr className={hasDetail ? 'has-detail' : undefined}>
                {shownColumns.map((column) => (
                  <td key={column.header} className={cellClass(column)}>
                    {column.cell(row)}
                  </td>
                ))}
              </tr>
              {hasDetail && <DetailRow row={row} columns={detailColumns} span={shownColumns.length} />}
            </Fragment>
          ))}
        </tbody>
      </table>
    </div>
  );
}
