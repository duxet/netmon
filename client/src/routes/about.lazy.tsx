import {createLazyFileRoute} from '@tanstack/react-router'
import {useQuery, useQueryClient} from "@tanstack/react-query";

export const Route = createLazyFileRoute('/about')({
  component: About,
})

function About() {
  return (
    <div className="p-2">
      <h3>Welcome Home!</h3>
    </div>
  )
}
