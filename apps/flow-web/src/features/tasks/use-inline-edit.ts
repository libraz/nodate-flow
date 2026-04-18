/**
 * useInlineEdit — lightweight state for inline cell editing in the task
 * list DataGrid. Tracks which (row, column) pair is currently being edited
 * so that only one cell can be in edit mode at a time.
 */

import { useState } from 'react';

export interface EditingCell {
  rowId: string;
  column: string;
}

export interface InlineEditState {
  editingCell: EditingCell | null;
  startEdit: (rowId: string, column: string) => void;
  stopEdit: () => void;
  isEditing: (rowId: string, column: string) => boolean;
}

export function useInlineEdit(): InlineEditState {
  const [editingCell, setEditingCell] = useState<EditingCell | null>(null);

  const startEdit = (rowId: string, column: string) => {
    setEditingCell({ rowId, column });
  };

  const stopEdit = () => {
    setEditingCell(null);
  };

  const isEditing = (rowId: string, column: string) =>
    editingCell?.rowId === rowId && editingCell?.column === column;

  return { editingCell, startEdit, stopEdit, isEditing };
}
