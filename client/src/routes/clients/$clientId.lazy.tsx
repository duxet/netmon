import {createLazyFileRoute} from '@tanstack/react-router'

export const Route = createLazyFileRoute('/clients/$clientId')({
  component: () => <div>Hello /clients/$clientId!</div>
})
