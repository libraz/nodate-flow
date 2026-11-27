import { createFileRoute } from '@tanstack/react-router';
import CalendarRoute from '../features/calendar-events/calendar-page';

export const Route = createFileRoute('/_authenticated/calendar')({
  component: CalendarRoute,
});
