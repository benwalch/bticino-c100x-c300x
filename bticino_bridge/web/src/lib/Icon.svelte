<script>
  // Draws an icon node from the `lucide` package: a list of [tag, attributes].
  export let icon
  export let size = 18
</script>

<svg
  xmlns="http://www.w3.org/2000/svg"
  width={size}
  height={size}
  viewBox="0 0 24 24"
  fill="none"
  stroke="currentColor"
  stroke-width="2"
  stroke-linecap="round"
  stroke-linejoin="round"
  aria-hidden="true"
>
  {#each icon as [tag, attrs]}
    <svelte:element this={tag} {...attrs} />
  {/each}
</svg>
