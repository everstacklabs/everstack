/**
 * Tree-shaken ECharts core.
 *
 * The full `echarts` barrel is ~1MB. We never want that in the bundle, so this
 * module is the ONE place that pulls from `echarts/core` and registers only the
 * chart types + components our dashboards actually use. Everything else in the
 * UI package and the apps imports the branded `EChart` wrapper, never echarts
 * directly, so this registry is the single seam controlling bundle weight.
 *
 * If a new chart needs a renderer/component that is not registered here (e.g.
 * `PieChart`, `VisualMapComponent`), add it to the `use([...])` call below and
 * nowhere else.
 */
import * as echarts from 'echarts/core'
import { BarChart, LineChart, ScatterChart } from 'echarts/charts'
import {
    AxisPointerComponent,
    DataZoomComponent,
    DataZoomInsideComponent,
    GraphicComponent,
    GridComponent,
    LegendComponent,
    MarkAreaComponent,
    MarkLineComponent,
    TooltipComponent,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

echarts.use([
    // chart types
    LineChart,
    BarChart,
    ScatterChart,
    // layout + interaction components
    GridComponent,
    TooltipComponent,
    AxisPointerComponent,
    LegendComponent,
    DataZoomComponent,
    DataZoomInsideComponent,
    MarkAreaComponent,
    MarkLineComponent,
    GraphicComponent,
    // renderer
    CanvasRenderer,
])

export { echarts }
