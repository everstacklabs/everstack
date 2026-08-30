import { createFileRoute } from '@tanstack/react-router'
import { HomePage } from '@/components/home/home-page'

type OnboardingPathParam = 'agent' | 'gateway' | 'production'

// Onboarding wizard state is reflected in the URL so steps are deep-linkable
// and survive refresh / back-forward navigation.
export interface HomeSearch {
  obPath?: OnboardingPathParam
  obStep?: string
}

export const Route = createFileRoute('/')({
  validateSearch: (search: Record<string, unknown>): HomeSearch => {
    const obPath = search.obPath
    return {
      obPath:
        obPath === 'agent' || obPath === 'gateway' || obPath === 'production' ? obPath : undefined,
      obStep: typeof search.obStep === 'string' ? search.obStep : undefined,
    }
  },
  component: HomePage,
})
