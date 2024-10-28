import { createLazyFileRoute } from '@tanstack/react-router'
import {useQuery} from "@tanstack/react-query";
import {formatBytes, formatNumber} from "../utils/formatters.ts";

export const Route = createLazyFileRoute('/')({
  component: Index,
})

async function getStats() {
    const response = await fetch('http://localhost:2137/api/stats');
    return await response.json();
}

function Index() {
    const query = useQuery({ queryKey: ['stats'], queryFn: getStats })

  return (
      <>
          <div className="max-w-[85rem] px-4 py-10 sm:px-6 lg:px-8 lg:py-14 mx-auto">
              <div className="grid sm:grid-cols-2 lg:grid-cols-4 gap-4 sm:gap-6">
                  <div
                      className="flex flex-col bg-white border shadow-sm rounded-xl dark:bg-slate-900 dark:border-gray-800">
                      <div className="p-4 md:p-5">
                          <div className="flex items-center gap-x-2">
                              <p className="text-xs uppercase tracking-wide text-gray-500">
                                  Clients
                              </p>
                              <div className="hs-tooltip">
                                  <div className="hs-tooltip-toggle">
                                      <svg className="flex-shrink-0 size-4 text-gray-500"
                                           xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"
                                           fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"
                                           stroke-linejoin="round">
                                          <circle cx="12" cy="12" r="10"/>
                                          <path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3"/>
                                          <path d="M12 17h.01"/>
                                      </svg>
                                      <span
                                          className="hs-tooltip-content hs-tooltip-shown:opacity-100 hs-tooltip-shown:visible opacity-0 transition-opacity inline-block absolute invisible z-10 py-1 px-2 bg-gray-900 text-xs font-medium text-white rounded shadow-sm dark:bg-slate-700"
                                          role="tooltip">
                The number of daily users
              </span>
                                  </div>
                              </div>
                          </div>

                          <div className="mt-1 flex items-center gap-x-2">
                              <h3 className="text-xl sm:text-2xl font-medium text-gray-800 dark:text-gray-200">
                                  {query.data?.TotalClients}
                              </h3>
                          </div>
                      </div>
                  </div>

                  <div
                      className="flex flex-col bg-white border shadow-sm rounded-xl dark:bg-slate-900 dark:border-gray-800">
                      <div className="p-4 md:p-5">
                          <div className="flex items-center gap-x-2">
                              <p className="text-xs uppercase tracking-wide text-gray-500">
                                  Bytes
                              </p>
                          </div>

                          <div className="mt-1 flex items-center gap-x-2">
                              <h3 className="text-xl sm:text-2xl font-medium text-gray-800 dark:text-gray-200">
                                  {formatBytes(query.data?.TotalBytes || 0)}
                              </h3>
                          </div>
                      </div>
                  </div>

                  <div
                      className="flex flex-col bg-white border shadow-sm rounded-xl dark:bg-slate-900 dark:border-gray-800">
                      <div className="p-4 md:p-5">
                          <div className="flex items-center gap-x-2">
                              <p className="text-xs uppercase tracking-wide text-gray-500">
                                  Packets
                              </p>
                          </div>

                          <div className="mt-1 flex items-center gap-x-2">
                              <h3 className="text-xl sm:text-2xl font-medium text-gray-800 dark:text-gray-200">
                                  {formatNumber(query.data?.TotalPackets || 0)}
                              </h3>
                          </div>
                      </div>
                  </div>

                  <div
                      className="flex flex-col bg-white border shadow-sm rounded-xl dark:bg-slate-900 dark:border-gray-800">
                      <div className="p-4 md:p-5">
                          <div className="flex items-center gap-x-2">
                              <p className="text-xs uppercase tracking-wide text-gray-500">
                                  TBD
                              </p>
                          </div>

                          <div className="mt-1 flex items-center gap-x-2">
                              <h3 className="text-xl sm:text-2xl font-medium text-gray-800 dark:text-gray-200">
                                  0
                              </h3>
                          </div>
                      </div>
                  </div>
              </div>
          </div>
      </>

  )
}
