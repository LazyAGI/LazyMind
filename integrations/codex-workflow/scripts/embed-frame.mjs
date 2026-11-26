import { embedPresentation } from '../../workflow-agent-core/node/embed-presentation.mjs'
import { startPresentationFrame } from '../dist/diagnostics/presentation-frame.mjs'

await embedPresentation(startPresentationFrame)
