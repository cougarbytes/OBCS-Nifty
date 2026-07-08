<script setup lang="ts"></script>
<template>
  <div class="relative w-full overflow-x-auto">
    <table :class="cn('w-full caption-bottom text-sm')"><slot /></table>
  </div>
</template>
