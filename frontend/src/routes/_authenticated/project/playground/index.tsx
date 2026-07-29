import { createFileRoute } from '@tanstack/react-router';
import { PLAYGROUND_SCOPE_REQUIREMENTS } from '@/config/route-permission';
import { ProjectGuard } from '@/components/project-guard';
import { RouteGuard } from '@/components/route-guard';
import Playground from '@/features/playground';

function ProtectedPlayground() {
  return (
    <ProjectGuard>
      <RouteGuard scopeRequirements={PLAYGROUND_SCOPE_REQUIREMENTS}>
        <Playground />
      </RouteGuard>
    </ProjectGuard>
  );
}

export const Route = createFileRoute('/_authenticated/project/playground/')({
  component: ProtectedPlayground,
});
