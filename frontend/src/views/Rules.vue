<template>
  <RuleVue
    v-model="ruleModal.visible"
    :visible="ruleModal.visible"
    :index="ruleModal.index"
    :data="ruleModal.data"
    :clients="clients"
    :inTags="inboundTags"
    :outTags="outboundTags"
    :rsTags="rulesetTags"
    @close="closeRuleModal"
    @save="saveRuleModal"
  />
  <RulesetVue
    v-model="rulesetModal.visible"
    :visible="rulesetModal.visible"
    :index="rulesetModal.index"
    :data="rulesetModal.data"
    :outTags="outboundTags"
    @close="closeRulesetModal"
    @save="saveRulesetModal"
  />
  <v-row>
    <v-col cols="12" justify="center" align="center">
      <v-btn color="primary" @click="showRuleModal(-1)" style="margin: 0 5px;">{{ $t('rule.add') }}</v-btn>
      <v-btn color="primary" @click="showRulesetModal(-1)" style="margin: 0 5px;">{{ $t('ruleset.add') }}</v-btn>
      <v-btn variant="outlined" color="warning" @click="saveConfig" :loading="loading" :disabled="stateChange">
        {{ $t('actions.save') }}
      </v-btn>
    </v-col>
  </v-row>
    <v-row>
    <v-col class="v-card-subtitle" cols="12">{{ $t('basic.routing.title') }} </v-col>
    <v-col cols="12">
        <v-row>
          <v-col cols="12" sm="6" md="3" lg="2">
            <v-select
              hide-details
              :label="$t('basic.routing.defaultOut')"
              clearable
              @click:clear="delete route.final"
              :items="outboundTags"
              v-model="route.final">
            </v-select>
          </v-col>
          <v-col cols="12" sm="6" md="3" lg="2">
            <v-text-field
              v-model="route.default_interface"
              hide-details
              clearable
              @click:clear="delete route.default_interface"
              :label="$t('basic.routing.defaultIf')"
            ></v-text-field>
          </v-col>
          <v-col cols="12" sm="6" md="3" lg="2">
            <v-text-field
              v-model.number="routeMark"
              hide-details
              type="number"
              min="0"
              :label="$t('basic.routing.defaultRm')"
            ></v-text-field>
          </v-col>
          <v-col cols="12" sm="6" md="3" lg="2">
            <v-switch
              v-model="route.auto_detect_interface"
              color="primary"
              :label="$t('basic.routing.autoBind')"
              hide-details>
            </v-switch>
          </v-col>
        </v-row>
      </v-col>
  </v-row>
  <v-row>
    <v-col class="v-card-subtitle" cols="12">
      <v-row align="center" no-gutters>
        <v-col cols="auto">{{ $t('rule.ruleset') }}</v-col>
        <v-spacer></v-spacer>
        <v-col cols="12" sm="8" md="6" lg="4" class="d-flex ga-2 align-center">
          <v-text-field
            v-model="searchRulesets"
            :label="$t('search') || 'Search'"
            prepend-inner-icon="mdi-magnify"
            variant="outlined"
            density="compact"
            hide-details
            clearable
            class="flex-grow-1"
            @keyup.enter="blurActive"
          ></v-text-field>
          <v-btn color="primary" size="small" @click="blurActive">{{ $t('actions.search') || 'Search' }}</v-btn>
        </v-col>
        <v-col cols="6" sm="4" md="3" lg="2">
          <v-select
            v-model="filterRulesetTag"
            :items="rulesetTags"
            :label="$t('objects.tag') || 'Tag'"
            variant="outlined"
            density="compact"
            hide-details
            clearable
          ></v-select>
        </v-col>
      </v-row>
      <v-row v-if="selectedRulesets.length > 0" class="mt-2" no-gutters>
        <v-col cols="auto" class="d-flex align-center ga-2">
          <v-chip color="primary" variant="tonal">已选 {{ selectedRulesets.length }}</v-chip>
          <v-btn color="error" size="small" variant="outlined" @click="batchDelRulesetConfirm = true">
            {{ $t('actions.deleteSelected') || 'Delete Selected' }}
          </v-btn>
          <v-btn size="small" variant="outlined" @click="selectedRulesets = []">{{ $t('actions.clearSelection') || 'Clear' }}</v-btn>
        </v-col>
      </v-row>
    </v-col>
    <v-col cols="12" sm="4" md="3" lg="2" v-for="(entry, fidx) in filteredRulesets" :key="entry.item.tag">
      <v-card rounded="xl" elevation="5" min-width="200" :title="entry.item.tag">
        <v-checkbox
          :model-value="selectedRulesets.includes(entry.item.tag)"
          @update:model-value="toggleRulesetSelect(entry.item.tag, !!$event)"
          hide-details
          density="compact"
          class="ml-2 mt-1"
        ></v-checkbox>
        <v-card-subtitle style="margin-top: -20px;">
          <v-row>
            <v-col>{{ $t('ruleset.' + entry.item.type) }}</v-col>
          </v-row>
        </v-card-subtitle>
        <v-card-text>
          <v-row>
            <v-col>{{ $t('ruleset.format') }}</v-col>
            <v-col>
              {{ entry.item.format }}
            </v-col>
          </v-row>
          <v-row>
            <v-col>{{ $t('actions.update') }}</v-col>
            <v-col>
              {{ entry.item.update_interval?? '-' }}
            </v-col>
          </v-row>
        </v-card-text>
        <v-divider></v-divider>
        <v-card-actions style="padding: 0;">
          <v-btn icon="mdi-file-edit" @click="showRulesetModal(entry.index)">
            <v-icon />
            <v-tooltip activator="parent" location="top" :text="$t('actions.edit')"></v-tooltip>
          </v-btn>
          <v-btn icon="mdi-file-remove" style="margin-inline-start:0;" color="warning" @click="delRulesetOverlay[entry.index] = true">
            <v-icon />
            <v-tooltip activator="parent" location="top" :text="$t('actions.del')"></v-tooltip>
          </v-btn>
          <v-overlay
            v-model="delRulesetOverlay[entry.index]"
            contained
            class="align-center justify-center"
          >
            <v-card :title="$t('actions.del')" rounded="lg">
              <v-divider></v-divider>
              <v-card-text>{{ $t('confirm') }}</v-card-text>
              <v-card-actions>
                <v-btn color="error" variant="outlined" @click="delRuleset(entry.index)">{{ $t('yes') }}</v-btn>
                <v-btn color="success" variant="outlined" @click="delRulesetOverlay[entry.index] = false">{{ $t('no') }}</v-btn>
              </v-card-actions>
            </v-card>
          </v-overlay>
        </v-card-actions>
      </v-card>
    </v-col>
  </v-row>
  <v-row>
    <v-col class="v-card-subtitle" cols="12">
      <v-row align="center" no-gutters>
        <v-col cols="auto">{{ $t('pages.rules') }}</v-col>
        <v-spacer></v-spacer>
        <v-col cols="12" sm="8" md="6" lg="4" class="d-flex ga-2 align-center">
          <v-text-field
            v-model="searchRules"
            :label="$t('search') || 'Search'"
            prepend-inner-icon="mdi-magnify"
            variant="outlined"
            density="compact"
            hide-details
            clearable
            class="flex-grow-1"
            @keyup.enter="blurActive"
          ></v-text-field>
          <v-btn color="primary" size="small" @click="blurActive">{{ $t('actions.search') || 'Search' }}</v-btn>
        </v-col>
        <v-col cols="6" sm="4" md="3" lg="2">
          <v-select
            v-model="filterRuleOutbound"
            :items="outboundTags"
            :label="$t('objects.outbound') || 'Outbound'"
            variant="outlined"
            density="compact"
            hide-details
            clearable
          ></v-select>
        </v-col>
        <v-col cols="6" sm="4" md="3" lg="2">
          <v-select
            v-model="filterRuleAction"
            :items="ruleActions"
            :label="$t('action') || 'Action'"
            variant="outlined"
            density="compact"
            hide-details
            clearable
          ></v-select>
        </v-col>
        <v-col cols="6" sm="4" md="3" lg="2">
          <v-select
            v-model="filterRuleType"
            :items="ruleTypes"
            :label="$t('type') || 'Type'"
            variant="outlined"
            density="compact"
            hide-details
            clearable
          ></v-select>
        </v-col>
      </v-row>
      <v-row v-if="selectedRules.length > 0" class="mt-2" no-gutters>
        <v-col cols="auto" class="d-flex align-center ga-2">
          <v-chip color="primary" variant="tonal">已选 {{ selectedRules.length }}</v-chip>
          <v-btn color="error" size="small" variant="outlined" @click="batchDelRuleConfirm = true">
            {{ $t('actions.deleteSelected') || 'Delete Selected' }}
          </v-btn>
          <v-btn size="small" variant="outlined" @click="selectedRules = []">{{ $t('actions.clearSelection') || 'Clear' }}</v-btn>
        </v-col>
      </v-row>
    </v-col>
    <v-col cols="12" sm="4" md="3" lg="2" v-for="(entry, fidx) in filteredRules"
        :key="entry.index"
        :draggable="true"
        @dragstart="onDragStart(entry.index)"
        @dragover.prevent
        @drop="onDrop(entry.index)"
      >
      <v-card rounded="xl" elevation="5" min-width="200" :title="entry.index+1">
        <v-checkbox
          :model-value="selectedRules.includes(entry.index)"
          @update:model-value="toggleRuleSelect(entry.index, !!$event)"
          hide-details
          density="compact"
          class="ml-2 mt-1"
        ></v-checkbox>
        <v-card-subtitle style="margin-top: -20px;">
          <v-row>
            <v-col>{{ entry.item.type != undefined ? $t('rule.logical') + ' (' + entry.item.mode + ')' : $t('rule.simple') }}</v-col>
          </v-row>
        </v-card-subtitle>
        <v-card-text>
          <v-row>
            <v-col>{{ $t('admin.action') }}</v-col>
            <v-col>
              {{ entry.item.action }}
            </v-col>
          </v-row>
          <v-row>
            <v-col>{{ $t('objects.outbound') }}</v-col>
            <v-col>
              {{ entry.item.outbound?? '-' }}
            </v-col>
          </v-row>
          <v-row>
            <v-col>{{ $t('pages.rules') }}</v-col>
            <v-col>
              {{ entry.item.rules ? entry.item.rules.length : Object.keys(entry.item).filter(r => !actionKeys.includes(r)).length }}
            </v-col>
          </v-row>
          <v-row>
            <v-col>{{ $t('rule.invert') }}</v-col>
            <v-col>
              {{ $t( (entry.item.invert?? false)? 'yes' : 'no') }}
            </v-col>
          </v-row>
        </v-card-text>
        <v-divider></v-divider>
        <v-card-actions style="padding: 0;">
          <v-btn icon="mdi-file-edit" @click="showRuleModal(entry.index)">
            <v-icon />
            <v-tooltip activator="parent" location="top" :text="$t('actions.edit')"></v-tooltip>
          </v-btn>
          <v-btn icon="mdi-file-remove" style="margin-inline-start:0;" color="warning" @click="delRuleOverlay[entry.index] = true">
            <v-icon />
            <v-tooltip activator="parent" location="top" :text="$t('actions.del')"></v-tooltip>
          </v-btn>
          <v-overlay
            v-model="delRuleOverlay[entry.index]"
            contained
            class="align-center justify-center"
          >
            <v-card :title="$t('actions.del')" rounded="lg">
              <v-divider></v-divider>
              <v-card-text>{{ $t('confirm') }}</v-card-text>
              <v-card-actions>
                <v-btn color="error" variant="outlined" @click="delRule(entry.index)">{{ $t('yes') }}</v-btn>
                <v-btn color="success" variant="outlined" @click="delRuleOverlay[entry.index] = false">{{ $t('no') }}</v-btn>
              </v-card-actions>
            </v-card>
          </v-overlay>
        </v-card-actions>
      </v-card>
    </v-col>
  </v-row>
  <!-- Batch delete confirm dialogs -->
  <v-dialog v-model="batchDelRulesetConfirm" max-width="400">
    <v-card :title="$t('actions.del')" rounded="lg">
      <v-divider></v-divider>
      <v-card-text>{{ $t('actions.confirmDeleteSelected', { count: selectedRulesets.length }) || `Delete ${selectedRulesets.length} selected rulesets?` }}</v-card-text>
      <v-card-actions>
        <v-spacer></v-spacer>
        <v-btn color="error" variant="outlined" @click="batchDelRulesets">{{ $t('yes') }}</v-btn>
        <v-btn color="success" variant="outlined" @click="batchDelRulesetConfirm = false">{{ $t('no') }}</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
  <v-dialog v-model="batchDelRuleConfirm" max-width="400">
    <v-card :title="$t('actions.del')" rounded="lg">
      <v-divider></v-divider>
      <v-card-text>{{ $t('actions.confirmDeleteSelected', { count: selectedRules.length }) || `Delete ${selectedRules.length} selected rules?` }}</v-card-text>
      <v-card-actions>
        <v-spacer></v-spacer>
        <v-btn color="error" variant="outlined" @click="batchDelRules">{{ $t('yes') }}</v-btn>
        <v-btn color="success" variant="outlined" @click="batchDelRuleConfirm = false">{{ $t('no') }}</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script lang="ts" setup>
import Data from '@/store/modules/data'
import { computed, ref, onMounted } from 'vue'
import RuleVue from '@/layouts/modals/Rule.vue'
import RulesetVue from '@/layouts/modals/Ruleset.vue'
import { Config } from '@/types/config'
import { actionKeys, ruleset } from '@/types/rules'
import { FindDiff } from '@/plugins/utils'

const oldConfig = ref({})
const loading = ref(false)

const appConfig = computed((): Config => {
  return <Config> Data().config
})

onMounted(async () => {
  // Defer deep clone so page renders first
  setTimeout(() => {
    oldConfig.value = JSON.parse(JSON.stringify(Data().config))
  }, 0)
})

const routeMark = computed({
  get() { return route.value.default_mark?? 0 },
  set(v:number) { v>0 ? route.value.default_mark = v : delete appConfig.value.route.default_mark }
})

const stateChange = computed(() => {
  return FindDiff.deepCompare(appConfig.value,oldConfig.value)
})

const saveConfig = async () => {
  loading.value = true
  const success = await Data().save("config", "set", appConfig.value)
  if (success) {
    oldConfig.value = JSON.parse(JSON.stringify(Data().config))
    loading.value = false
  }
}

const clients = computed((): string[] => {
  return Data().clients.map((c:any) => c.name)
})

const route = computed((): any => {
  return appConfig.value.route?? {}
})

const rules = computed((): any[] => {
  const data = route.value
  if (!data){
    return []
  }
  if (!('rules' in data) || !Array.isArray(data.rules)) {
    data.rules = []
  }
  return data.rules
})

const rulesets = computed((): any[] => {
  const data = route.value
  if (!data){
    return []
  }
  if (!('rule_set' in data) || !Array.isArray(data.rule_set)) {
    data.rule_set = []
  }
  return data.rule_set
})

const rulesetTags = computed((): any[] => {
  return rulesets.value.map((rs:any) => rs.tag)
})

const outboundTags = computed((): string[] => {
  return [...Data().outbounds?.map((o:any) => o.tag), ...Data().endpoints?.map((e:any) => e.tag)]
})

const inboundTags = computed((): string[] => {
  return [...Data().inbounds?.map((o:any) => o.tag), ...Data().endpoints?.filter((e:any) => e.listen_port > 0).map((e:any) => e.tag)]
})

let delRuleOverlay = ref(new Array<boolean>)
let delRulesetOverlay = ref(new Array<boolean>)

// Search & batch selection for rules/rulesets
const searchRules = ref('')
const searchRulesets = ref('')
const filterRulesetTag = ref<string | null>(null)
const filterRuleOutbound = ref<string | null>(null)
const filterRuleAction = ref<string | null>(null)
const filterRuleType = ref<string | null>(null)
const selectedRules = ref<number[]>([])
const selectedRulesets = ref<string[]>([])
const batchDelRuleConfirm = ref(false)
const batchDelRulesetConfirm = ref(false)

const blurActive = () => {
  (document.activeElement as HTMLElement)?.blur()
}

const filteredRulesets = computed((): {item: any, index: number}[] => {
  const all = rulesets.value.map((item: any, index: number) => ({ item, index }))
  const q = searchRulesets.value.toLowerCase()
  return all.filter(({ item }) => {
    if (filterRulesetTag.value && item.tag !== filterRulesetTag.value) return false
    if (!q) return true
    return (item.tag && item.tag.toLowerCase().includes(q)) ||
      (item.type && item.type.toLowerCase().includes(q))
  })
})

const ruleActions = computed((): string[] => {
  const s = new Set<string>()
  rules.value.forEach((r: any) => { if (r.action) s.add(String(r.action)) })
  return [...s].sort()
})
const ruleTypes = computed((): string[] => {
  const s = new Set<string>()
  rules.value.forEach((r: any) => { if (r.type) s.add(String(r.type)) })
  return [...s].sort()
})
const filteredRules = computed((): {item: any, index: number}[] => {
  const all = rules.value.map((item: any, index: number) => ({ item, index }))
  const q = searchRules.value.toLowerCase()
  return all.filter(({ item }) => {
    if (filterRuleOutbound.value && String(item.outbound) !== filterRuleOutbound.value) return false
    if (filterRuleAction.value && String(item.action) !== filterRuleAction.value) return false
    if (filterRuleType.value && String(item.type) !== filterRuleType.value) return false
    if (!q) return true
    return (item.outbound && String(item.outbound).toLowerCase().includes(q)) ||
      (item.action && String(item.action).toLowerCase().includes(q)) ||
      (item.type && String(item.type).toLowerCase().includes(q))
  })
})

const toggleRulesetSelect = (tag: string, val: boolean) => {
  if (val) {
    if (!selectedRulesets.value.includes(tag)) selectedRulesets.value.push(tag)
  } else {
    selectedRulesets.value = selectedRulesets.value.filter(t => t !== tag)
  }
}

const toggleRuleSelect = (index: number, val: boolean) => {
  if (val) {
    if (!selectedRules.value.includes(index)) selectedRules.value.push(index)
  } else {
    selectedRules.value = selectedRules.value.filter(i => i !== index)
  }
}

const batchDelRulesets = () => {
  const tags = new Set(selectedRulesets.value)
  // remove from the end to keep indices stable
  for (let i = rulesets.value.length - 1; i >= 0; i--) {
    if (tags.has(rulesets.value[i].tag)) rulesets.value.splice(i, 1)
  }
  selectedRulesets.value = []
  batchDelRulesetConfirm.value = false
}

const batchDelRules = () => {
  const idx = [...selectedRules.value].sort((a, b) => b - a)
  for (const i of idx) rules.value.splice(i, 1)
  selectedRules.value = []
  batchDelRuleConfirm.value = false
}

const ruleModal = ref({
  visible: false,
  index: -1,
  data: "",
})

const showRuleModal = (index: number) => {
  ruleModal.value.index = index
  ruleModal.value.data = index == -1 ? '' : JSON.stringify(rules.value[index])
  ruleModal.value.visible = true
}

const closeRuleModal = () => {
  ruleModal.value.visible = false
}

const saveRuleModal = (data:any) => {
  // New or Edit
  if (ruleModal.value.index == -1) {
    rules.value.push(data)
  } else {
    rules.value[ruleModal.value.index] = data
  }
  ruleModal.value.visible = false
}

const delRule = (index: number) => {
  rules.value.splice(index,1)
  delRuleOverlay.value[index] = false
}

const rulesetModal = ref({
  visible: false,
  index: -1,
  data: "",
})

const showRulesetModal = (index: number) => {
  rulesetModal.value.index = index
  rulesetModal.value.data = index == -1 ? '' : JSON.stringify(rulesets.value[index])
  rulesetModal.value.visible = true
}

const closeRulesetModal = () => {
  rulesetModal.value.visible = false
}

const saveRulesetModal = (data:ruleset) => {
  // New or Edit
  if (rulesetModal.value.index == -1) {
    rulesets.value.push(data)
  } else {
    rulesets.value[rulesetModal.value.index] = data
  }
  rulesetModal.value.visible = false
}

const delRuleset = (index: number) => {
  rulesets.value.splice(index,1)
  delRulesetOverlay.value[index] = false
}

const draggedItemIndex = ref(null)

const onDragStart = (index: any) => {
  draggedItemIndex.value = index
}

const onDrop = (index: any) => {
  if (draggedItemIndex.value !== null) {
    // Swap the dragged item with the dropped one
    const draggedItem = rules.value[draggedItemIndex.value]
    rules.value.splice(draggedItemIndex.value, 1)
    rules.value.splice(index, 0, draggedItem)
    draggedItemIndex.value = null
  }
}
</script>