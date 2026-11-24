import type { HTMLAttributes, TdHTMLAttributes, ThHTMLAttributes } from 'react';

type Align = 'left' | 'right' | 'center';

const alignClass = (align: Align | undefined) => (align ? ` table-${align}` : '');

export function Table({ children, ...props }: HTMLAttributes<HTMLTableElement>) {
  return <table {...props}>{children}</table>;
}

export function TableHeader({ children, ...props }: HTMLAttributes<HTMLTableSectionElement>) {
  return <thead {...props}>{children}</thead>;
}

export function TableBody({ children, ...props }: HTMLAttributes<HTMLTableSectionElement>) {
  return <tbody {...props}>{children}</tbody>;
}

export function TableRow({ children, selected, ...props }: HTMLAttributes<HTMLTableRowElement> & { selected?: boolean }) {
  return <tr {...props} data-selected={selected ? 'true' : undefined}>{children}</tr>;
}

export function TableHead({ align, alignRight, children, className, ...props }: ThHTMLAttributes<HTMLTableCellElement> & { align?: Align; alignRight?: boolean }) {
  return <th {...props} className={`${className ?? ''}${alignClass(alignRight ? 'right' : align)}`}>{children}</th>;
}

export function TableCell({ align, alignRight, children, className, ...props }: TdHTMLAttributes<HTMLTableCellElement> & { align?: Align; alignRight?: boolean }) {
  return <td {...props} className={`${className ?? ''}${alignClass(alignRight ? 'right' : align)}`}>{children}</td>;
}
